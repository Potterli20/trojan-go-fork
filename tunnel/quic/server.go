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
	activeConns sync.Map // map[*quic.Conn]*quic.Conn
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

	s.wg.Go(func() {
		var handlerSent bool
		defer close(packetDone)
		defer close(packetBuffer)
		for {
			// ReceiveDatagram 返回调用方自有的新切片(quic-go 内部 make+copy),
			// 无需再维护每连接 64KB 中转 buf 或手工 copy。
			data, err := conn.ReceiveDatagram(connCtx)
			if err != nil {
				log.Debug("QUIC message receive error:", err)
				return
			}
			if !handlerSent {
				handlerSent = true
				select {
				case s.packetChan <- &PacketConn{conn: conn, packetBuffer: packetBuffer}:
				case <-s.ctx.Done():
					return
				}
			}
			select {
			case packetBuffer <- data:
			case <-connCtx.Done():
				return
			}
		}
	})

	for {
		select {
		case stream, ok := <-streamChan:
			if !ok {
				return
			}
			streamTracker := log.NewConnectionTracker("QUIC", "Stream").
				WithField("remote_addr", conn.RemoteAddr().String()).
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
