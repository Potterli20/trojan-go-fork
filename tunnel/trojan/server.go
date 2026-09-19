package trojan

import (
	"context"
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

func acquireAuth(ctx context.Context, cfg *Config) *authInstance {
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
			log.Error(common.NewError("failed to create authenticator").Base(err))
			return nil
		}
		Auth = auth
		current = &authInstance{auth: auth, ctx: authCtx, cancel: cancel}
	}
	current.refcnt++
	return current
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
	log.Debug("Closing connection for user", c.hash, "RealIP", c.ipX, "from", c.Conn.RemoteAddr(), "tunneling to", c.metadata.Address,
		"sent:", common.HumanFriendlyTraffic(c.sent.Load()), "recv:", common.HumanFriendlyTraffic(c.recv.Load()))
	// Auth() 成功时会调用 AddIP(RealIP) 占用 IP 槽，关闭时必须释放，
	// 否则只能靠 memory.User 的 10 秒过期扫描清理，短连接频繁切换 IP 时会提前触顶 MaxIPNum。
	// c.user 可能为 nil（Close 在 Auth 之前被调用），需做 nil 检查。
	if c.user != nil {
		c.user.DelIP(c.ipX)
	}
	return c.Conn.Close()
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
		return common.NewError("invalid hash:" + string(userHash[:]))
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
	// wg.Wait 之后所有连接和 api 服务都已退出，此时才能安全释放共享认证器；
	// 否则 SQLite/MySQL 句柄与流量汇总协程会一直泄露到进程结束。
	// Close 可能被上层重复调用，引用计数只能归还一次。
	s.closeOnce.Do(func() {
		releaseAuth(s.authRef)
	})
	return err
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.underlay.AcceptConn(&Tunnel{})
		if err != nil {
			// 不能用 "select ctx.Done / default" 判断关闭：ctx 恰已取消时两分支随机命中，
			// 会漏记错误；直接检查 ctx.Err()
			if s.ctx.Err() != nil {
				return
			}
			log.Error(common.NewError("trojan failed to accept conn").Base(err))
			continue
		}
		s.wg.Go(func() {
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
		})
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
	ctx, cancel := context.WithCancel(ctx)

	ref := acquireAuth(ctx, cfg)
	if ref == nil {
		cancel()
		return nil, common.NewError("trojan failed to create authenticator")
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
		redirConn, err := net.Dial("tcp", redirAddr.String())
		if err != nil {
			cancel()
			releaseAuth(ref)
			return nil, common.NewError("invalid redirect address. check your http server: " + redirAddr.String()).Base(err)
		}
		redirConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	}

	if cfg.API.Enabled {
		s.wg.Go(func() {
			api.RunService(ctx, Name+"_SERVER", auth)
		})
	}

	s.wg.Go(func() {
		s.acceptLoop()
	})
	log.Debug("trojan server created")
	return s, nil
}
