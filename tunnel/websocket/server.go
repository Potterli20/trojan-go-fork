package websocket

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/redirector"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/Potterli20/trojan-go-fork/tunnel/transport"
)

// handshakeTimeout 限定整个 WebSocket 握手窗口的时限：既包括等对端把升级请求
// 发完，也包括把 101 响应写出去。两个方向都可能被对端拖着不动，缺一不可。
const handshakeTimeout = 30 * time.Second

type Server struct {
	underlay  tunnel.Server
	path      string
	enabled   bool
	redirAddr net.Addr
	redir     *redirector.Redirector
	ctx       context.Context
	cancel    context.CancelFunc

	// 在途握手登记。连接一旦被 AcceptConn 取走，下层就再也握不到它的句柄：
	// 队列排空、监听关闭都够不着。握手卡住时只有这份登记表能让 Close 生效。
	pendingMutex sync.Mutex
	pendingConns map[tunnel.Conn]struct{}
	closing      bool
}

// trackPending 登记一条还在握手里的连接，返回 false 表示服务已进入关停，
// 调用方应立刻关掉该连接并退出（closing 与登记在同一把锁下判定，
// 避免和 closePending 的遍历互相插队）。
func (s *Server) trackPending(conn tunnel.Conn) bool {
	s.pendingMutex.Lock()
	defer s.pendingMutex.Unlock()
	if s.closing {
		return false
	}
	s.pendingConns[conn] = struct{}{}
	return true
}

func (s *Server) untrackPending(conn tunnel.Conn) {
	s.pendingMutex.Lock()
	delete(s.pendingConns, conn)
	s.pendingMutex.Unlock()
}

// closePending 置 closing 并关掉所有在途握手的连接。关闭动作在锁外做，
// 避免和被叫醒的 AcceptConn 里 untrackPending 的加锁互相等待。
func (s *Server) closePending() {
	s.pendingMutex.Lock()
	s.closing = true
	pending := make([]tunnel.Conn, 0, len(s.pendingConns))
	for c := range s.pendingConns {
		pending = append(pending, c)
	}
	s.pendingMutex.Unlock()

	for _, c := range pending {
		c.Close() //gosec:disable -- 错误忽略：关停路径叫醒阻塞在握手响应上的写
	}
}

func (s *Server) Close() error {
	s.cancel()
	err := s.underlay.Close()
	// 先关掉在途握手的连接：只靠 handshakeTimeout(30s) 的时限自己退出的话，
	// Close 要白等半分钟，真实进程里只会被 proxy 的 5s 兜底遮住，看似"停机慢一点"。
	// 顺带这条连接在被关掉之前一直占着 fd、TLS 会话状态和一个 accept 协程。
	s.closePending()
	// 重定向器只有在这里被显式关闭才会取消它的 ctx：worker 不排空队列就退出，
	// 队列里（容量 64）和在中继中的会话都攥着未认证对端的活连接，最长要等满
	// redirectionIdleTimeout 才松开。
	s.redir.Close() //gosec:disable -- Redirector.Close 恒返回 nil；关停路径尽力回收
	return err
}

func (s *Server) cleanupFailedHandshake(conn tunnel.Conn, tracker *log.ConnectionTracker, err error) error {
	if transportConn, ok := conn.(*transport.Conn); ok {
		if rewindConn, ok := transportConn.Conn.(*common.RewindConn); ok {
			rewindConn.Rewind()
			rewindConn.StopBuffering()
		}
	}
	_ = tracker.Error(err)
	s.redir.Redirect(&redirector.Redirection{
		InboundConn: conn,
		RedirectTo:  s.redirAddr,
	})
	return err
}

// acceptHandshake 是 websocket.Accept 的注入点。真实实现不用 ctx——coder 的 Accept
// 只从响应写入器和请求上工作；ctx 存在的唯一理由是让判据能拿到握手期间创建的那个
// 子 ctx，从而在库内 panic 时验证它确实被取消了（handshake_panic_test.go）。
// 参照 tunnel/tproxy 的 `var dialUDP = DialUDP`：这类注入点是刻意留给测试的，别当冗余删掉。
var acceptHandshake = func(ctx context.Context, w http.ResponseWriter, req *http.Request, opts *websocket.AcceptOptions) (*websocket.Conn, error) {
	return websocket.Accept(w, req, opts)
}

// readUpgradeRequest 是 http.ReadRequest 的注入点，存在理由与下面的 acceptHandshake 一样：
// 判据（handshake_panic_test.go）要能在"解析对端可控的升级请求"这一步制造 panic，而那一步
// 发生在 trackPending **之前**——没有注入点就测不到这条窗口里的资源收尾。别当冗余删掉。
var readUpgradeRequest = func(r *bufio.Reader) (*http.Request, error) {
	return http.ReadRequest(r)
}

func (s *Server) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	conn, err := s.underlay.AcceptConn(&Tunnel{})
	if err != nil {
		return nil, common.NewError("websocket failed to accept connection from underlying server")
	}

	// 本函数里 remote_addr 是不变量，却被算了 6 遍（其中一处是 Debugf 的实参，
	// 级别关掉时照旧求值）。RemoteAddr().String() 实测 125 ns、3 allocs 一次，
	// 统一提出来算一次；Debugf 那条也顺带不再无条件格式化。
	remoteAddr := conn.RemoteAddr().String()

	tracker := log.NewConnectionTracker("WebSocket", "AcceptConn").
		WithField("remote_addr", remoteAddr).
		WithField("path", s.path)

	log.Debugf("[WebSocket] [conn=%s] New connection accepted from %s, path=%s, enabled=%v",
		tracker.ConnID(), remoteAddr, s.path, s.enabled)

	// panic 出口的资源收尾。本函数解析的是**未认证对端可控**的升级请求，而父层的
	// tunnel.AcceptConnGuarded 只把 panic 变成 error——它手里没有这条连接的句柄。
	// 在 trackPending 之前 panic（下面的 ReadRequest / 头部校验那几行）的话，这条连接
	// 既不在在途登记表里（Server.Close 的排空够不着）、也没有任何活引用留下，fd 只能等
	// 运行时对 netFD 的 finalizer 回收，TLS 会话状态和 bufio 读写缓冲随之滞留。
	// 凡是把所有权交出去的出口都必须先置位 connHandedOff：移交上游的会话要长期持有它，
	// 而 cleanupFailedHandshake 会把同一条连接交给 redirector 继续拷贝——在这里关掉它
	// 等于对诱饵站自曝（口径同 shadowsocks 的 takenOver）。
	connHandedOff := false
	defer func() {
		if !connHandedOff {
			conn.Close() //gosec:disable -- 错误忽略：本层即将丢掉这条连接
		}
	}()
	// fail 是唯一认的失败出口：走这里说明连接已经交给 redirector，收尾的关闭逻辑必须让位。
	fail := func(err error) (tunnel.Conn, error) {
		connHandedOff = true
		return nil, s.cleanupFailedHandshake(conn, tracker, err)
	}

	if !s.enabled {
		err := common.NewError("websocket is disabled. redirecting http request from " + remoteAddr)
		return fail(err)
	}
	// rw 会被 websocket 库继续用来读升级后的帧，所以这里用可解除的限幅：
	// 只在解析升级请求的窗口内限制读取量（http.ReadRequest 自身没有 header 上限），
	// 解析一结束就交还，不影响后续数据流。
	bounded := common.NewBoundedReader(conn)
	bounded.Limit(common.MaxSniffRequestBytes)
	rw := bufio.NewReadWriter(bufio.NewReader(bounded), bufio.NewWriter(conn))
	// 握手窗口内双向限时。原先只设了读时限，读完请求就把写方向裸交出去：
	// 对端把升级请求发完之后不再读取任何一个字节（TCP 零窗口）时，下面
	// websocket.Accept 里写 101 响应的 Flush 会永久阻塞。AcceptConn 是被入站的
	// accept 循环串行调用的，一条卡死等于这个入站从此不再接纳任何新连接；
	// 而这条连接已经离开下层的待处理队列，排空逻辑再也够不着它攥着的 fd。
	conn.SetDeadline(time.Now().Add(handshakeTimeout)) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	req, err := readUpgradeRequest(rw.Reader)
	bounded.Unlimited()
	if err != nil {
		conn.SetDeadline(time.Time{}) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		log.Debug("invalid http request")
		err = common.NewError("not a valid http request: " + remoteAddr).Base(err)
		return fail(err)
	}
	if strings.ToLower(req.Header.Get("Upgrade")) != "websocket" || req.URL.Path != s.path {
		conn.SetDeadline(time.Time{}) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		log.Debug("invalid http websocket handshake request")
		err = common.NewError("not a valid websocket handshake request: " + remoteAddr).Base(err)
		return fail(err)
	}

	// 登记在途后才会被 Close 关掉；没有这一步，阻塞在 101 写上的 AcceptConn
	// 只能等满 30s 时限，期间这个入站不再接纳任何新连接。
	if !s.trackPending(conn) {
		// 不在这儿手写 Close：让上面那条 panic 收尾的 defer 统一负责，
		// 免得两处关闭以后又有人加第三条。
		return nil, common.NewError("websocket server is closing")
	}
	connCtx, cancel := context.WithCancel(s.ctx)
	// 握手期间的三件收尾（注销在途登记、解除握手时限、取消子 ctx）必须成对，
	// 而且要在 panic 下也跑完。原先是三段裸顺序调用：Accept 解析的是对端可控的
	// 升级请求，一旦库内 panic，pendingConns 的表项和这个挂在 s.ctx 下的子 ctx
	// 就永久留着——cancel 没人调，父 ctx 不退出它就不断子节点，每命中一次多一份，
	// 直到进程结束。（调用方那侧另有一道边界：accept 循环里的 recover 早于本修复
	// 存在，且**不在**这条调用链上——它只兜 per-conn handler，见 tunnel/accept_guard.go。）
	handedOff := false
	defer func() {
		if !handedOff {
			cancel()
		}
	}()
	var wsConn *websocket.Conn
	err = func() error {
		defer s.untrackPending(conn)
		// 连接要么移交上游长期持有，要么在这之后被关掉重定向，两种都要解除握手时限；
		// 留在原地的话，一条正常会话会在 30s 后被自己没做完的握手截止时间掐断。
		defer conn.SetDeadline(time.Time{}) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		var err error
		wsConn, err = acceptHandshake(connCtx, &fakeHTTPResponseWriter{Conn: conn, ReadWriter: rw}, req, &websocket.AcceptOptions{
			// Host/Origin 不匹配是代理场景常态(如 CDN 回源),鉴权由 trojan 层完成
			InsecureSkipVerify: true,
		})
		return err
	}()
	if err != nil {
		err = common.NewError("websocket failed to handshake: " + remoteAddr).Base(err)
		return fail(err)
	}
	// NetConn 会把读上限重置为 -1,必须在 NetConn 之后重新设置
	stream := websocket.NetConn(connCtx, wsConn, websocket.MessageBinary)
	wsConn.SetReadLimit(maxMessageSize)

	_ = tracker.Success()
	log.Debugf("[WebSocket] [conn=%s] Handshake completed, remote=%s", tracker.ConnID(), remoteAddr)
	handedOff = true
	connHandedOff = true
	return &InboundConn{
		Conn:    stream,
		tcpConn: conn,
		request: req,
		cancel:  cancel,
		tracker: tracker,
	}, nil
}

func (s *Server) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, common.NewError("not supported")
}

func NewServer(ctx context.Context, underlay tunnel.Server) (*Server, error) {
	cfg := config.FromContext(ctx, Name).(*Config)
	if cfg.Websocket.Enabled {
		if !strings.HasPrefix(cfg.Websocket.Path, "/") {
			return nil, common.NewError("websocket path must start with \"/\"")
		}
	}
	if cfg.RemoteHost == "" {
		log.Warn("empty websocket redirection hostname")
		cfg.RemoteHost = cfg.Websocket.Host
	}
	if cfg.RemotePort == 0 {
		log.Warn("empty websocket redirection port")
		cfg.RemotePort = 80
	}
	// remote-addr 与 websocket.host 都为空时不能再往下走：拼出来的诱饵地址是 ":80"，
	// 而实测 net.Dial("tcp", ":80") 连的是 127.0.0.1 —— 未认证对端的字节会被中继进
	// 代理自己主机的本地服务。两次 Warn 之后必须有闸，否则这份配置永远没有症状。
	if cfg.RemoteHost == "" {
		return nil, common.NewError("invalid websocket redirection address: both remote-addr and websocket.host are empty")
	}
	ctx, cancel := context.WithCancel(ctx)
	log.Debug("websocket server created")

	return &Server{
		enabled:      cfg.Websocket.Enabled,
		path:         cfg.Websocket.Path,
		ctx:          ctx,
		cancel:       cancel,
		underlay:     underlay,
		redir:        redirector.NewRedirector(ctx),
		redirAddr:    tunnel.NewAddressFromHostPort("tcp", cfg.RemoteHost, cfg.RemotePort),
		pendingConns: make(map[tunnel.Conn]struct{}),
	}, nil
}

// fakeHTTPResponseWriter 模拟 http.ResponseWriter:coder 的 Accept 依赖
// WriteHeader 输出 101 升级响应,再通过 Hijack 取出连接进行帧读写;
// 失败路径(http.Error)的错误响应在此丢弃,保持底层流干净以便重定向
type fakeHTTPResponseWriter struct {
	Conn       net.Conn
	ReadWriter *bufio.ReadWriter
	header     http.Header
	wrote101   bool
}

func (w *fakeHTTPResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *fakeHTTPResponseWriter) WriteHeader(code int) {
	if code != http.StatusSwitchingProtocols || w.wrote101 {
		return
	}
	w.wrote101 = true
	// 手写 101 响应头,避免 http.Response.Write 注入 Content-Length 等干扰头;
	// 响应留在 bufio.Writer 缓冲中,由后续帧写出时一并 flush,顺序正确
	var buf bytes.Buffer
	buf.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	for key, values := range w.Header() {
		for _, value := range values {
			buf.WriteString(key)
			buf.WriteString(": ")
			buf.WriteString(value)
			buf.WriteString("\r\n")
		}
	}
	buf.WriteString("\r\n")
	_, _ = w.ReadWriter.Writer.Write(buf.Bytes())
	// 101 必须立即上线:coder 只在写帧时 flush,而握手后双方都无帧可写,
	// 不主动 flush 客户端将等不到升级响应
	_ = w.ReadWriter.Writer.Flush()
}

func (*fakeHTTPResponseWriter) Write([]byte) (int, error) {
	return 0, io.EOF
}

func (w *fakeHTTPResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.Conn, w.ReadWriter, nil
}
