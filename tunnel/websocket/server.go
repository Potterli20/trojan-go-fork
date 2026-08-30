package websocket

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/redirector"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/Potterli20/trojan-go-fork/tunnel/transport"
)

// handshakeTimeout 限定等待客户端发出 WebSocket 升级请求的时限
const handshakeTimeout = 30 * time.Second

type Server struct {
	underlay  tunnel.Server
	path      string
	enabled   bool
	redirAddr net.Addr
	redir     *redirector.Redirector
	ctx       context.Context
	cancel    context.CancelFunc
}

func (s *Server) Close() error {
	s.cancel()
	return s.underlay.Close()
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

func (s *Server) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	conn, err := s.underlay.AcceptConn(&Tunnel{})
	if err != nil {
		return nil, common.NewError("websocket failed to accept connection from underlying server")
	}

	tracker := log.NewConnectionTracker("WebSocket", "AcceptConn").
		WithField("remote_addr", conn.RemoteAddr().String()).
		WithField("path", s.path)

	log.Debugf("[WebSocket] [conn=%s] New connection accepted from %s, path=%s, enabled=%v",
		tracker.ConnID(), conn.RemoteAddr().String(), s.path, s.enabled)

	if !s.enabled {
		err := common.NewError("websocket is disabled. redirecting http request from " + conn.RemoteAddr().String())
		return nil, s.cleanupFailedHandshake(conn, tracker, err)
	}
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	// 等待 WebSocket 升级请求限时:对端静默时不让本调用永久阻塞
	conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	req, err := http.ReadRequest(rw.Reader)
	conn.SetReadDeadline(time.Time{})
	if err != nil {
		log.Debug("invalid http request")
		err = common.NewError("not a valid http request: " + conn.RemoteAddr().String()).Base(err)
		return nil, s.cleanupFailedHandshake(conn, tracker, err)
	}
	if strings.ToLower(req.Header.Get("Upgrade")) != "websocket" || req.URL.Path != s.path {
		log.Debug("invalid http websocket handshake request")
		err = common.NewError("not a valid websocket handshake request: " + conn.RemoteAddr().String()).Base(err)
		return nil, s.cleanupFailedHandshake(conn, tracker, err)
	}

	connCtx, cancel := context.WithCancel(s.ctx)
	wsConn, err := websocket.Accept(&fakeHTTPResponseWriter{Conn: conn, ReadWriter: rw}, req, &websocket.AcceptOptions{
		// Host/Origin 不匹配是代理场景常态(如 CDN 回源),鉴权由 trojan 层完成
		InsecureSkipVerify: true,
	})
	if err != nil {
		cancel()
		err = common.NewError("websocket failed to handshake: " + conn.RemoteAddr().String()).Base(err)
		return nil, s.cleanupFailedHandshake(conn, tracker, err)
	}
	// NetConn 会把读上限重置为 -1,必须在 NetConn 之后重新设置
	stream := websocket.NetConn(connCtx, wsConn, websocket.MessageBinary)
	wsConn.SetReadLimit(maxMessageSize)

	_ = tracker.Success()
	log.Debugf("[WebSocket] [conn=%s] Handshake completed, remote=%s", tracker.ConnID(), conn.RemoteAddr().String())
	return &InboundConn{
		OutboundConn: OutboundConn{
			Conn:    stream,
			tcpConn: conn,
			request: req,
		},
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
	ctx, cancel := context.WithCancel(ctx)
	log.Debug("websocket server created")

	return &Server{
		enabled:   cfg.Websocket.Enabled,
		path:      cfg.Websocket.Path,
		ctx:       ctx,
		cancel:    cancel,
		underlay:  underlay,
		redir:     redirector.NewRedirector(ctx),
		redirAddr: tunnel.NewAddressFromHostPort("tcp", cfg.RemoteHost, cfg.RemotePort),
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
