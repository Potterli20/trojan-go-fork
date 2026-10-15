package quic

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	tlstunnel "github.com/Potterli20/trojan-go-fork/tunnel/tls"
	"github.com/Potterli20/trojan-go-fork/tunnel/tls/fingerprint"
	"github.com/apernet/quic-go"
)

type Server struct {
	listener    *quic.Listener
	ctx         context.Context
	cancel      context.CancelFunc
	underlay    tunnel.Server
	connChan    chan tunnel.Conn
	packetChan  chan tunnel.PacketConn
	localAddr   *tunnel.Address
	quicConfig  *quic.Config
	tlsConfig   *tls.Config
	congestion  string
	brutalUp    uint64
	brutalDown  uint64
	activeConns sync.Map       // map[*quic.Conn]*quic.Conn
	packetConn  net.PacketConn // 我们自己 ListenUDP 出来的 socket，必须自己关
	wg          sync.WaitGroup
}

func (s *Server) applyCongestionControl(conn *quic.Conn) {
	ApplyCongestionControl(conn, CongestionConfig{
		Algorithm:  s.congestion,
		BrutalUp:   s.brutalUp,
		BrutalDown: s.brutalDown,
	}, "server")
}

func (s *Server) Close() error {
	s.cancel()
	s.listener.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	// quic.Listen 用的是我们传进去的 PacketConn，而 Transport 只在 createdConn
	// 为真时才关它（quic-go transport.go:488，否则只设一次读截止把读循环叫醒），
	// 所以这个 UDP socket 必须由本层释放：否则每个 Server 实例漏一个 fd，端口也
	// 一直被占，同进程里重建监听会 bind 失败。
	if s.packetConn != nil {
		s.packetConn.Close() //gosec:disable -- 错误忽略：关停路径，尽力释放
	}
	s.wg.Wait()
	s.activeConns.Range(func(_, value any) bool {
		value.(*quic.Conn).CloseWithError(quic.ApplicationErrorCode(0), "server closed") //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		return true
	})
	if s.underlay != nil {
		return s.underlay.Close()
	}
	return nil
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept(s.ctx)
		if err != nil {
			// 不能用 "select ctx.Done / default" 判断关闭：ctx 恰已取消时两分支随机命中；
			// 直接检查 ctx.Err()
			if s.ctx.Err() != nil {
				log.Debug("QUIC accept loop stopped")
			} else {
				log.Error(common.NewError("QUIC accept error").Base(err))
			}
			return
		}

		s.applyCongestionControl(conn)

		tracker := log.NewConnectionTracker("QUIC", "AcceptConn").
			WithField("remote_addr", conn.RemoteAddr().String()).
			WithField("congestion", s.congestion)

		s.activeConns.Store(conn, conn)
		log.Debugf("[QUIC] [conn=%s] New connection accepted from %s, congestion=%s, alpn=%v",
			tracker.ConnID(), conn.RemoteAddr(), s.congestion, s.tlsConfig.NextProtos)

		s.wg.Go(func() {
			s.handleConnection(conn, tracker)
		})
	}
}

func (s *Server) handleConnection(conn *quic.Conn, tracker *log.ConnectionTracker) {
	defer func() {
		conn.CloseWithError(quic.ApplicationErrorCode(0), "connection closed") //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		s.activeConns.Delete(conn)
		log.Debugf("[QUIC] [conn=%s] Connection closed from %s, duration=%s",
			tracker.ConnID(), conn.RemoteAddr(),
			time.Since(tracker.StartTime()))
	}()

	streamChan := make(chan *quic.Stream, 16)
	packetBuffer := make(chan []byte, 16)
	packetDone := make(chan struct{})
	// connCtx 由 handleConnection 的 defer connCancel() 结束:两个泵 goroutine 都
	// select connCtx,连接结束即唤醒阻塞的 AcceptStream/ReceiveDatagram,不留 parked goroutine。
	connCtx, connCancel := context.WithCancel(s.ctx)
	defer connCancel()

	s.wg.Go(func() {
		for {
			stream, err := conn.AcceptStream(connCtx)
			if err != nil {
				log.Debug("QUIC stream accept error:", err)
				close(streamChan)
				return
			}
			select {
			case streamChan <- stream:
			case <-connCtx.Done():
				return
			}
		}
	})

	// 这一条 PacketConn 在泵启动前就构造好：泵要靠它的 done 退出。
	// 服务端形态没有 ctx/cancel（读的是 packetBuffer 而不是直读数据报），
	// 过去 Close() 等于空操作，relay 拆掉会话（proxy.go 的 defer Close）之后
	// 没有任何东西再排空 packetBuffer —— 泵写满 16 个就永久阻塞在发送上，
	// 而 connCtx 只在 handleConnection 返回时取消、handleConnection 又等 packetDone，
	// 于是 1 个 goroutine + 16 个数据报缓冲 + activeConns 条目一起被钉住，
	// 这条连接的 UDP 后续全丢。
	udpSession := &PacketConn{conn: conn, packetBuffer: packetBuffer, done: make(chan struct{})}

	s.wg.Go(func() {
		var handlerSent bool
		defer close(packetBuffer)
		// packetDone 只表示"这条连接到头了"。会话级关闭（udpSession.done）刻意不点它：
		// handleConnection 见到 packetDone 就 return，而它的 defer 会把整个连接
		// CloseWithError 掉——殃及同一条连接上还在正常工作的 stream。
		connectionEnding := false
		defer func() {
			if connectionEnding {
				close(packetDone)
			}
		}()
		for {
			// ReceiveDatagram 返回调用方自有的新切片(quic-go 内部 make+copy),
			// 无需再维护每连接 64KB 中转 buf 或手工 copy。
			data, err := conn.ReceiveDatagram(connCtx)
			if err != nil {
				log.Debug("QUIC message receive error:", err)
				connectionEnding = true
				return
			}
			// 空数据报不上交：详见 client.go 里同名的过滤点
			if len(data) == 0 {
				continue
			}
			if !handlerSent {
				handlerSent = true
				select {
				case s.packetChan <- udpSession:
				case <-s.ctx.Done():
					connectionEnding = true
					return
				case <-udpSession.done:
					return
				}
			}
			select {
			case packetBuffer <- data:
			case <-connCtx.Done():
				connectionEnding = true
				return
			case <-udpSession.done:
				// 上游 relay 已经关掉这条 UDP 会话：停止转发即可，连接本身留给 stream 继续用。
				// 不从这里出去的话，泵会带着 16 个数据报缓冲长期占住一个 goroutine，
				// 而这条连接后续的 UDP 全部丢弃。
				return
			}
		}
	})

	// remote_addr 在整条连接上是同一个值（quic-go 的 Conn.RemoteAddr() 读的是建链时存下的
	// 地址），但 String() 每次都要重新格式化：实测 125 ns、3 allocs 一次。这条循环是
	// 每个 stream 走一遍，所以提到循环外算一次。
	remoteAddr := conn.RemoteAddr().String()

	for {
		select {
		case stream, ok := <-streamChan:
			if !ok {
				return
			}
			streamTracker := log.NewConnectionTracker("QUIC", "Stream").
				WithField("remote_addr", remoteAddr).
				WithField("parent_conn", tracker.ConnID())
			log.Debugf("[QUIC] [conn=%s] New stream accepted, parent_conn=%s",
				streamTracker.ConnID(), tracker.ConnID())
			select {
			case s.connChan <- &StreamConn{Stream: stream, conn: conn, tracker: streamTracker}:
			case <-s.ctx.Done():
				return
			}

		case <-packetDone:
			// 数据报读结束(通常为连接级 datagram 队列关闭):仅结束本连接的 UDP 会话,
			// 不再顺带拆掉同连接上仍活跃的 stream——保持原语义从简,这里仅返回结束 handler。
			return

		case <-s.ctx.Done():
			return
		}
	}
}

func (s *Server) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	select {
	case conn := <-s.connChan:
		return conn, nil
	case <-s.ctx.Done():
		return nil, common.NewError("QUIC server closed")
	}
}

func (s *Server) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	select {
	case conn := <-s.packetChan:
		return conn, nil
	case <-s.ctx.Done():
		return nil, common.NewError("QUIC server closed")
	}
}

func NewServer(ctx context.Context, underlay tunnel.Server) (*Server, error) {
	cfg := config.FromContext(ctx, Name).(*Config)
	tlsCfg := config.FromContext(ctx, tlstunnel.Name).(*tlstunnel.Config)

	if cfg.RemoteHost == "" {
		return nil, common.NewError("QUIC listen address is empty")
	}

	if cfg.QUIC.ALPN == "" {
		cfg.QUIC.ALPN = "hq-29"
	}

	localAddr := tunnel.NewAddressFromHostPort("udp", cfg.RemoteHost, cfg.RemotePort)

	var keyPair tls.Certificate
	var err error
	if tlsCfg.TLS.CertPath != "" && tlsCfg.TLS.KeyPath != "" {
		keyPair, err = tls.LoadX509KeyPair(tlsCfg.TLS.CertPath, tlsCfg.TLS.KeyPath)
		if err != nil {
			return nil, common.NewError("QUIC failed to load key pair").Base(err)
		}
	} else {
		return nil, common.NewError("QUIC requires TLS certificate and key")
	}

	tlsConfig := &tls.Config{
		Certificates:     []tls.Certificate{keyPair},
		NextProtos:       []string{cfg.QUIC.ALPN},
		CurvePreferences: fingerprint.ParseCurvePreferences(tlsCfg.TLS.CurvePreferences),
	}

	quicConfig := &quic.Config{
		MaxIdleTimeout:     time.Second * time.Duration(cfg.QUIC.MaxIdleTimeout),
		MaxIncomingStreams: int64(cfg.QUIC.MaxIncomingStreams),
		// 与 client 对称开启 datagram 支持,否则对端数据报无法收发。
		EnableDatagrams: true,
		// initial_stream_window / initial_conn_window 之前定义了却从未接线，用户设了
		// 不生效。0 表示交给 quic-go 的库内默认（512KB）。
		InitialStreamReceiveWindow:     uint64(cfg.QUIC.InitialStreamWindow),
		InitialConnectionReceiveWindow: uint64(cfg.QUIC.InitialConnWindow),
	}

	packetConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(cfg.RemoteHost), Port: cfg.RemotePort})
	if err != nil {
		return nil, common.NewError("QUIC failed to listen UDP").Base(err)
	}

	listener, err := quic.Listen(packetConn, tlsConfig, quicConfig)
	if err != nil {
		packetConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		return nil, common.NewError("QUIC failed to listen").Base(err)
	}

	log.Debug("QUIC server congestion control:", cfg.QUIC.Congestion)
	if cfg.QUIC.Congestion == "brutal" || cfg.QUIC.Congestion == "force-brutal" {
		log.Debug("QUIC server brutal_up:", cfg.QUIC.BrutalUp, "bps")
		log.Debug("QUIC server brutal_down:", cfg.QUIC.BrutalDown, "bps")
	}

	ctx, cancel := context.WithCancel(ctx)
	server := &Server{
		listener:   listener,
		packetConn: packetConn,
		ctx:        ctx,
		cancel:     cancel,
		underlay:   underlay,
		connChan:   make(chan tunnel.Conn, 32),
		packetChan: make(chan tunnel.PacketConn, 8),
		localAddr:  localAddr,
		quicConfig: quicConfig,
		tlsConfig:  tlsConfig,
		congestion: cfg.QUIC.Congestion,
		brutalUp:   cfg.QUIC.BrutalUp,
		brutalDown: cfg.QUIC.BrutalDown,
	}

	server.wg.Go(func() {
		server.acceptLoop()
	})
	log.Info("QUIC server listening on", localAddr.String())
	return server, nil
}
