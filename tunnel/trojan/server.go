package trojan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Potterli20/trojan-go-fork/api"
	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/recorder"
	"github.com/Potterli20/trojan-go-fork/redirector"
	"github.com/Potterli20/trojan-go-fork/statistic"
	"github.com/Potterli20/trojan-go-fork/statistic/memory"
	"github.com/Potterli20/trojan-go-fork/statistic/mysql"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/Potterli20/trojan-go-fork/tunnel/mux"
	"github.com/Potterli20/trojan-go-fork/tunnel/websocket"
)

var (
	// Auth 是包级共享的认证器:多个 Server 实例(test/scenario 里反复
	// NewServer)并发创建时,check-then-set 需要互斥保护
	authMu  sync.Mutex
	Auth    statistic.Authenticator
	current *authInstance
)

// authInstance 是包级共享认证器的引用计数包装。
// 每个 Server 持有自己的实例指针，所以即使测试把 Auth 置 nil 触发重建，
// 旧实例也只由创建它的那些 Server 负责关闭。
type authInstance struct {
	auth   statistic.Authenticator
	ctx    context.Context // statistic.createdAuth 的键，释放时用于移除
	cancel context.CancelFunc
	refcnt int
}

// acquireAuth 取（或按需重建）包级共享认证器。失败时把**原始原因**返回给调用方：
// 以前这里是 log.Error 之后 return nil，调用点只能凭空造一句
// "trojan failed to create authenticator"，用户拿到手的错误链里就没有根因了
// （实测配置文件写空密码时，最后两行日志只剩 "trojan failed to create authenticator"，
// 真正那句 "password #1 is empty..." 只在中间一行 ERROR 里）。
func acquireAuth(ctx context.Context, cfg *Config) (*authInstance, error) {
	authMu.Lock()
	defer authMu.Unlock()
	if Auth == nil || current == nil || current.auth != Auth {
		// 认证器的 ctx 不能寄生在首个 Server 的 ctx 上：那个 ctx 一旦被取消，
		// 共享认证器的后台协程（流量汇总、MySQL 轮询）会先于其他使用者退出。
		authCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		var auth statistic.Authenticator
		var err error
		if cfg.MySQL.Enabled {
			log.Debug("mysql enabled")
			auth, err = statistic.NewAuthenticator(authCtx, mysql.Name)
		} else {
			log.Debug("auth by config file")
			auth, err = statistic.NewAuthenticator(authCtx, memory.Name)
		}
		if err != nil {
			cancel()
			return nil, common.NewError("failed to create authenticator").Base(err)
		}
		Auth = auth
		current = &authInstance{auth: auth, ctx: authCtx, cancel: cancel}
	}
	current.refcnt++
	return current, nil
}

// releaseAuth 归还一次引用；只有最后一个使用者会真正关闭认证器。
// 必须在所有 handler 和 api 服务退出之后调用，否则会有连接还在读已关闭的用户表。
func releaseAuth(inst *authInstance) {
	if inst == nil {
		return
	}
	authMu.Lock()
	if inst.refcnt > 0 {
		inst.refcnt--
	}
	if inst.refcnt > 0 {
		authMu.Unlock()
		return
	}
	inst.cancel()
	if current == inst {
		current = nil
		if Auth == inst.auth {
			Auth = nil
		}
	}
	authMu.Unlock()

	// ReleaseAuthenticator 会连同 createdAuth 注册表一起移除，避免全局 map 留住已关闭实例
	if err := statistic.ReleaseAuthenticator(inst.ctx); err != nil {
		log.Error(common.NewError("failed to release authenticator").Base(err))
	}
}

// authTimeout 限定读取 trojan 认证头（56 字节 hash + 地址）的时限
const authTimeout = 10 * time.Second

// InboundConn is a trojan inbound connection
type InboundConn struct {
	// WARNING: do not change the order of these fields.
	// 64-bit fields that use `sync/atomic` package functions
	// must be 64-bit aligned on 32-bit systems.
	// Reference: https://github.com/golang/go/issues/599
	// Solution: https://github.com/golang/go/issues/11891#issuecomment-433623786
	sent atomic.Uint64
	recv atomic.Uint64

	net.Conn
	auth         statistic.Authenticator
	user         statistic.User
	hash         string
	metadata     *tunnel.Metadata
	ip           string
	ipX          string
	trustHeaders bool
	// closeOnce 保证 Close 的副作用只跑一次：trojan 的 UDP 会话里 packet.go 读到
	// 错误时会 Close 一次，relay 的 defer inbound.Close() 还会再 Close 一次，
	// 而 InboundConn.Close 每次都调 user.DelIP(ipX)。DelIP 是按调用次数递减的 IP
	// 引用计数，多减一次会提前释放槽位、绕过 MaxIPNum（同源 IP 上还有别的活跃会话时）。
	closeOnce sync.Once
}

func (c *InboundConn) Metadata() *tunnel.Metadata {
	return c.metadata
}

func (c *InboundConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n >= 0 {
		c.sent.Add(uint64(n)) //gosec:disable -- n >= 0 已保证安全
	}
	// 流量方向必须与 API 契约一致：Sent = 服务端发出 = 用户下载（API 的 DownloadTraffic，受 SendLimiter 限制），
	// Recv = 服务端收到 = 用户上传（API 的 UploadTraffic，受 RecvLimiter 限制）。对调会导致上下行限速互相打反。
	c.user.AddSentTraffic(n)
	return n, err
}

func (c *InboundConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n >= 0 {
		c.recv.Add(uint64(n)) //gosec:disable -- n >= 0 已保证安全
	}
	c.user.AddRecvTraffic(n)
	return n, err
}

func (c *InboundConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		log.Debug("Closing connection for user", c.hash, "RealIP", c.ipX, "from", c.Conn.RemoteAddr(), "tunneling to", c.metadata.Address,
			"sent:", common.HumanFriendlyTraffic(c.sent.Load()), "recv:", common.HumanFriendlyTraffic(c.recv.Load()))
		// Auth() 成功时会调用 AddIP(RealIP) 占用 IP 槽，关闭时必须释放，
		// 否则只能靠 memory.User 的 10 秒过期扫描清理，短连接频繁切换 IP 时会提前触顶 MaxIPNum。
		// c.user 可能为 nil（Close 在 Auth 之前被调用），需做 nil 检查。
		if c.user != nil {
			c.user.DelIP(c.ipX)
		}
		err = c.Conn.Close()
	})
	return err
}

func extractRealIPFromHeaders(header http.Header) string {
	if cfIP := header.Get("CF-Connecting-IP"); cfIP != "" {
		return cfIP
	}
	if xff := header.Get("X-Forwarded-For"); xff != "" {
		firstIP, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(firstIP)
	}
	return ""
}

func GetRealIP(c *InboundConn) string {
	// 默认不信任请求头里的 CF-Connecting-IP / X-Forwarded-For：
	// 若 websocket 端点直接暴露（无前置可信代理/CDN），攻击者可伪造这些 header 绕过 ip_limit。
	// 仅当 Server 配置 trust_headers=true 时才解析 header。
	if !c.trustHeaders {
		return c.ip
	}

	var request *http.Request

	switch conn := c.Conn.(type) {
	case *websocket.InboundConn:
		request = conn.OutboundConn.Request()
	case *common.RewindConn:
		if wsConn, ok := conn.Conn.(*websocket.InboundConn); ok {
			request = wsConn.OutboundConn.Request()
		}
	default:
		log.Debug("Failed to convert to WebSocket or RewindConn")
		return c.ip
	}

	if request != nil {
		if realIP := extractRealIPFromHeaders(request.Header); realIP != "" {
			return realIP
		}
	}
	return c.ip
}

func (c *InboundConn) Auth() error {
	userHash := [56]byte{}
	// 单次 Read 不保证读满 56 字节，TCP 分段时合法客户端会被误判；
	// 连接带 authTimeout 截止时间，不会永久阻塞
	if _, err := io.ReadFull(c.Conn, userHash[:]); err != nil {
		return common.NewError("failed to read hash").Base(err)
	}

	valid, user := c.auth.AuthUser(string(userHash[:]))
	if !valid {
		// 不要把对端给的原始 56 字节拼进错误串：它经 .Base() 会进 Warn 日志，而这是
		// 未认证输入（可含换行/控制字符）——既是日志注入面，hash 本身也等价凭据。
		return common.NewError("invalid hash")
	}
	c.hash = string(userHash[:])
	c.user = user

	ip, _, err := net.SplitHostPort(c.Conn.RemoteAddr().String())
	if err != nil {
		return common.NewError("failed to parse host:" + c.Conn.RemoteAddr().String()).Base(err)
	}
	c.ip = ip
	RealIP := GetRealIP(c)
	c.ipX = RealIP
	ok := user.AddIP(RealIP)
	if !ok {
		return common.NewError("ip limit reached for RealIP: " + RealIP)
	}

	crlf := [2]byte{}
	_, err = io.ReadFull(c.Conn, crlf[:])
	if err != nil {
		c.rollbackIPLimit()
		return err
	}

	c.metadata = &tunnel.Metadata{}
	_, err = c.metadata.ReadFrom(c.Conn)
	if err != nil {
		c.rollbackIPLimit()
		return err
	}

	_, err = io.ReadFull(c.Conn, crlf[:])
	if err != nil {
		c.rollbackIPLimit()
		return err
	}
	return nil
}

// rollbackIPLimit 在 Auth 后续步骤失败时回滚已占用的 IP 槽，
// 防止认证失败场景下 IP 槽泄漏导致 MaxIPNum 被耗尽
func (c *InboundConn) rollbackIPLimit() {
	if c.user != nil && c.ipX != "" {
		c.user.DelIP(c.ipX)
	}
}

func (c *InboundConn) Record() {
	log.Debug("user", c.hash, "from", c.Conn.RemoteAddr(), "tunneling to", c.metadata.Address)
	recorder.Add(c.hash, c.Conn.RemoteAddr(), c.metadata.Address, "TCP", nil)
}

func (c *InboundConn) Hash() string {
	return c.hash
}

// Server is a trojan tunnel server
type Server struct {
	auth         statistic.Authenticator
	authRef      *authInstance
	redir        *redirector.Redirector
	redirAddr    *tunnel.Address
	underlay     tunnel.Server
	connChan     chan tunnel.Conn
	muxChan      chan tunnel.Conn
	packetChan   chan tunnel.PacketConn
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	closeOnce    sync.Once
	trustHeaders bool
}

func (s *Server) Close() error {
	s.cancel()
	// 先关闭底层 transport 再等待 acceptLoop 退出：
	// acceptLoop 阻塞在 underlay.AcceptConn 上，若先 wg.Wait() 会永久死锁
	err := s.underlay.Close()
	s.wg.Wait()
	// wg.Wait 之后 acceptLoop 已退出、不再向 channel 发送，排空已完成握手但还没
	// 被上层取走的连接，否则这些连接会带着 fd 和已认证的用户身份滞留到进程结束
drain:
	for {
		select {
		case c := <-s.connChan:
			c.Close() //gosec:disable -- 关闭滞留连接,忽略 close 错误
		case c := <-s.muxChan:
			c.Close() //gosec:disable -- 关闭滞留连接,忽略 close 错误
		case p := <-s.packetChan:
			p.Close() //gosec:disable -- 关闭滞留连接,忽略 close 错误
		default:
			break drain
		}
	}
	// wg.Wait 之后所有连接和 api 服务都已退出，此时才能安全释放共享认证器；
	// 否则 SQLite/MySQL 句柄与流量汇总协程会一直泄露到进程结束。
	// Close 可能被上层重复调用，引用计数只能归还一次。
	s.closeOnce.Do(func() {
		releaseAuth(s.authRef)
	})
	// 重定向器必须跟着一起收：worker 走 ctx.Done 分支只 return、不排空队列，而它的
	// ctx 只有 Redirector.Close 会取消。漏掉这一步，关停瞬间排在队列里（容量 64）的
	// 请求、以及已经在中继的会话都还攥着未认证对端的活连接，最长要等满
	// redirectionIdleTimeout(60s) 才自己松开——每层入站都漏一批 fd。
	s.redir.Close() //gosec:disable -- Redirector.Close 恒返回 nil；关停路径尽力回收
	return err
}

// 并发握手的工作池大小。下层的 AcceptConn 里跑着 tls 握手、websocket 的
// `http.ReadRequest` + `websocket.Accept`、shadowsocks 的密文探测——全都在等
// **未认证对端**发字节。旧写法把这次调用写在 accept 循环体里串行执行：一条只完成
// TCP/TLS 连接却始终不发数据的对端，就能让整个入站停摆最长 30s（ws 握手时限），
// 慢速扫描即可让所有合法用户连不上——同型缺陷在 `tunnel/adapter`（2026-10-20，
// 判据 sniff_parallel_test.go）已经按"挪进 per-conn 协程"修过，这里是漏网的姊妹。
//
// 不写成"每 Accept 一次就派一个协程"，因为那样在途未认证握手的协程/fd 数就交给
// 对端决定了。固定 N 个 acceptor 既把握手并行化，又把它钉在一个常数上。
const acceptWorkers = 64

// acceptBackoffStep 与 acceptBackoffCap 是 accept 出错后的退避参数（线性、封顶 3s）。
// 下层永久坏掉时，N 个 acceptor 会各自转成满 CPU 热循环——socks 的同型问题实测
// 400ms 内读了 3500 万次，所以退避与日志限流必须一起加，不能只并行不背压。
const (
	acceptBackoffStep = 100 * time.Millisecond
	acceptBackoffCap  = 3 * time.Second
)

func (s *Server) acceptLoop() {
	for range acceptWorkers {
		s.wg.Go(s.acceptWorker)
	}
}

// acceptWorker 只负责"取一条已握手的连接"并交给 handler；握手本身在它自己的
// AcceptConn 调用里，因此一个静默对端最多占住一个 worker，而不是整个入站。
func (s *Server) acceptWorker() {
	failures := 0
	for {
		conn, err := tunnel.AcceptConnGuarded(s.underlay, &Tunnel{})
		if err != nil {
			// 下层（websocket 的升级请求解析、shadowsocks 的密文探测这一类）在
			// **未认证对端字节**上冒出的 panic 已经被边界收住：本层还活着，继续接纳。
			// 下面那个 per-conn recover 够不着握手本身，见 tunnel/accept_guard.go。
			isPanic := errors.Is(err, tunnel.ErrAcceptPanic)
			if s.ctx.Err() != nil {
				return
			}
			failures++
			// 限流：出错速率由对端决定，每条都记 Error 就等于把日志变成攻击面；
			// 每 10 次一条 Error，其余走门禁后再格式化（口径同 tunnel/socks）。
			if failures%10 == 1 {
				if isPanic {
					log.Error(common.NewError("trojan accept recovered from a panic in the underlay").Base(err))
				} else {
					log.Error(common.NewError("trojan failed to accept conn").Base(err))
				}
			} else if log.ShouldLog(log.DebugLevel) {
				log.Debug("trojan accept failed (suppressed) x", failures)
			}
			delay := min(time.Duration(failures)*acceptBackoffStep, acceptBackoffCap)
			select {
			case <-time.After(delay):
			case <-s.ctx.Done():
				return
			}
			continue
		}
		failures = 0
		s.wg.Go(func() {
			s.serveConn(conn)
		})
	}
}

// serveConn 就是一条已握手连接的处理流程（原 acceptLoop 里的闭包体，逐字搬过来）。
func (s *Server) serveConn(conn tunnel.Conn) {
	defer func() {
		if r := recover(); r != nil {
			log.Error(common.NewError("panic in trojan handler: " + fmt.Sprintf("%v", r)))
			conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		}
	}()

	rewindConn := common.NewRewindConn(conn)
	rewindConn.SetBufferSize(128)

	inboundConn := &InboundConn{
		Conn:         rewindConn,
		auth:         s.auth,
		trustHeaders: s.trustHeaders,
	}

	// 对端静默时不设截止时间会让 handler 永久阻塞在 Auth，Close() 的 wg.Wait() 随之挂起
	rewindConn.SetReadDeadline(time.Now().Add(authTimeout)) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	if err := inboundConn.Auth(); err != nil {
		// 先 Rewind 保留已读字节供重定向方回放，再停止累积：
		// 只 Rewind 不 StopBuffering 会让重定向中继的全部流量
		// 继续被无界 append 进 buf，长连接下内存无限增长
		rewindConn.Rewind()
		rewindConn.StopBuffering()
		rewindConn.SetReadDeadline(time.Time{}) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		log.Warn(common.NewError("connection with invalid trojan header from " + rewindConn.RemoteAddr().String()).Base(err))
		s.redir.Redirect(&redirector.Redirection{
			RedirectTo:  s.redirAddr,
			InboundConn: rewindConn,
		})
		return
	}
	rewindConn.SetReadDeadline(time.Time{}) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理

	rewindConn.StopBuffering()
	switch inboundConn.metadata.Command {
	case Connect:
		if inboundConn.metadata.DomainName == "MUX_CONN" {
			select {
			case s.muxChan <- inboundConn:
				log.Debug("mux(r) connection")
			case <-s.ctx.Done():
				inboundConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			}
		} else {
			select {
			case s.connChan <- inboundConn:
				log.Debug("normal trojan connection")
				inboundConn.Record()
			case <-s.ctx.Done():
				inboundConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			}
		}

	case Associate:
		select {
		case s.packetChan <- &PacketConn{
			Conn: inboundConn,
		}:
			log.Debug("trojan udp connection")
		case <-s.ctx.Done():
			inboundConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		}
	case Mux:
		select {
		case s.muxChan <- inboundConn:
			log.Debug("mux connection")
		case <-s.ctx.Done():
			inboundConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		}
	default:
		log.Error(common.NewErrorf("unknown trojan command %d", inboundConn.metadata.Command))
		inboundConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	}
}

func (s *Server) AcceptConn(nextTunnel tunnel.Tunnel) (tunnel.Conn, error) {
	switch nextTunnel.(type) {
	case *mux.Tunnel:
		select {
		case t := <-s.muxChan:
			return t, nil
		case <-s.ctx.Done():
			return nil, common.NewError("trojan client closed")
		}
	default:
		select {
		case t := <-s.connChan:
			return t, nil
		case <-s.ctx.Done():
			return nil, common.NewError("trojan client closed")
		}
	}
}

func (s *Server) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	select {
	case t := <-s.packetChan:
		return t, nil
	case <-s.ctx.Done():
		return nil, common.NewError("trojan client closed")
	}
}

func NewServer(ctx context.Context, underlay tunnel.Server) (*Server, error) {
	cfg := config.FromContext(ctx, Name).(*Config)
	// API 暴露面检查放在**任何资源分配之前**（口径同 statistic/memory 拒绝空密码那条）：
	// api.RunService 的返回值在下面那个 goroutine 里是被丢掉的，策略性拒绝只有在这里
	// 同步返回才能让建栈失败、进程以非零退出码收场。放到后面就得补一堆 cancel/releaseAuth
	// 的清理分支，而且那时监听已经 bind 上了。
	if err := api.CheckExposure(ctx, Name+"_SERVER"); err != nil {
		return nil, err
	}

	// 诱饵地址缺主机名时必须拒绝启动，而不是拿空主机去拼地址：
	// NewAddressFromHostPort("tcp", "", port) 的 String() 是 ":port"，而实测
	// net.Dial("tcp", ":port") **连的是 127.0.0.1** —— 认证失败的流量会被代打到
	// 代理自己主机上的本地服务，且空域名匹配不上任何按域路由的规则。
	// disable-http-check: true 时下面那个探测不会跑，这条路径就完全是静默的。
	// 姊妹入站早就拦了（shadowsocks/quic 显式拒绝空 RemoteHost），trojan 是漏的那个。
	if cfg.RemoteHost == "" {
		return nil, common.NewError("invalid trojan redirection address: remote-addr is empty")
	}
	ctx, cancel := context.WithCancel(ctx)

	ref, err := acquireAuth(ctx, cfg)
	if err != nil {
		cancel()
		return nil, err
	}
	auth := ref.auth

	// 仅在显式配置 record_capacity 时覆盖包级默认值（10），
	// 否则把 Capacity 置 0 会导致 Subscribe 创建无缓冲 channel，
	// broadcast 的 select+default 会丢弃几乎所有 Record，录制功能静默失效。
	if cfg.RecordCapacity > 0 {
		recorder.SetCapacity(cfg.RecordCapacity)
	}

	redirAddr := tunnel.NewAddressFromHostPort("tcp", cfg.RemoteHost, cfg.RemotePort)
	s := &Server{
		underlay:     underlay,
		auth:         auth,
		authRef:      ref,
		redirAddr:    redirAddr,
		connChan:     make(chan tunnel.Conn, 64),       // 增加连接池大小
		muxChan:      make(chan tunnel.Conn, 64),       // 增加连接池大小
		packetChan:   make(chan tunnel.PacketConn, 64), // 增加连接池大小
		ctx:          ctx,
		cancel:       cancel,
		redir:        redirector.NewRedirector(ctx),
		trustHeaders: cfg.TrustHeaders,
	}

	if !cfg.DisableHTTPCheck {
		// 带超时且接 ctx 的探测拨号（口径同 redirector.defaultDial）：裸 net.Dial 在
		// 诱饵地址丢 SYN（黑洞 IP）时要挂满约 2 分钟才报错，而这是 NewServer 里的同步
		// 调用——建栈的主协程被钉住期间 Ctrl-C 与上层 cancel 全都无效，域名还会再叠一轮
		// 不看 ctx 的 DNS。这里的 ctx 已经是上面 WithCancel 派生出来的那一个。
		redirConn, err := common.Dial(ctx, common.DialConfig{
			Network: "tcp",
			Address: redirAddr.String(),
			Timeout: 30 * time.Second,
		})
		if err != nil {
			cancel()
			releaseAuth(ref)
			return nil, common.NewError("invalid redirect address. check your http server: " + redirAddr.String()).Base(err)
		}
		redirConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	}

	if cfg.API.Enabled {
		s.wg.Go(func() {
			// 返回值必须记下来：RunService 失败（如端口被占、地址解析失败）时只
			// return error，丢进 goroutine 就等于 API 悄悄不在、日志里一行都没有。
			if err := api.RunService(ctx, Name+"_SERVER", auth); err != nil {
				log.Error(common.NewError("server-side api service stopped").Base(err))
			}
		})
	}

	s.wg.Go(func() {
		s.acceptLoop()
	})
	log.Debug("trojan server created")
	return s, nil
}
