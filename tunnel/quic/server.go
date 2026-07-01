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
	"github.com/apernet/quic-go"
)

type Server struct {
	listener    any
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
	activeConns sync.Map
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
	s.wg.Wait()
	s.listener.(interface{ Close() error }).Close()
	s.activeConns.Range(func(key, value any) bool {
		value.(interface {
			CloseWithError(code uint32, reason string) error
		}).CloseWithError(0, "server closed")
		return true
	})
	if s.underlay != nil {
		return s.underlay.Close()
	}
	return nil
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.(interface {
			Accept(context.Context) (any, error)
		}).Accept(context.Background())
		if err != nil {
			select {
			case <-s.ctx.Done():
				log.Debug("QUIC accept loop stopped")
				return
			default:
				log.Error(common.NewError("QUIC accept error").Base(err))
				return
			}
		}

		quicConn := conn.(*quic.Conn)
		s.applyCongestionControl(quicConn)

		tracker := log.NewConnectionTracker("QUIC", "AcceptConn").
			WithField("remote_addr", quicConn.RemoteAddr().String()).
			WithField("congestion", s.congestion)

		s.activeConns.Store(quicConn.RemoteAddr().String(), conn)
		log.Debugf("[QUIC] [conn=%s] New connection accepted from %s, congestion=%s, alpn=%v",
			tracker.ConnID(), quicConn.RemoteAddr(), s.congestion, s.tlsConfig.NextProtos)

		s.wg.Go(func() {
			s.handleConnection(conn, tracker)
		})
	}
}

func (s *Server) handleConnection(conn any, tracker *log.ConnectionTracker) {
	defer func() {
		conn.(interface {
			CloseWithError(code uint32, reason string) error
		}).CloseWithError(0, "connection closed")
		s.activeConns.Delete(conn.(interface{ RemoteAddr() net.Addr }).RemoteAddr().String())
		log.Debugf("[QUIC] [conn=%s] Connection closed from %s, duration=%s",
			tracker.ConnID(), conn.(interface{ RemoteAddr() net.Addr }).RemoteAddr(),
			time.Since(tracker.StartTime()))
	}()

	streamChan := make(chan *quic.Stream, 16)
	packetBuffer := make(chan []byte, 16)
	connCtx, connCancel := context.WithCancel(s.ctx)
	defer connCancel()

	s.wg.Go(func() {
		for {
			stream, err := conn.(interface {
				AcceptStream(context.Context) (quic.Stream, error)
			}).AcceptStream(connCtx)
			if err != nil {
				log.Debug("QUIC stream accept error:", err)
				close(streamChan)
				return
			}
			streamPtr := &stream
			select {
			case streamChan <- streamPtr:
			case <-connCtx.Done():
				return
			}
		}
	})

	s.wg.Go(func() {
		buf := make([]byte, 65536)
		for {
			n, err := conn.(interface {
				ReceiveDatagram(context.Context, []byte) (int, error)
			}).ReceiveDatagram(connCtx, buf)
			if err != nil {
				log.Debug("QUIC message receive error:", err)
				close(packetBuffer)
				return
			}
			data := make([]byte, n)
			copy(data, buf[:n])
			select {
			case packetBuffer <- data:
			case <-connCtx.Done():
				return
			}
		}
	})

	hasPacketHandler := false

	for {
		select {
		case stream, ok := <-streamChan:
			if !ok {
				return
			}
			streamTracker := log.NewConnectionTracker("QUIC", "Stream").
				WithField("remote_addr", conn.(interface{ RemoteAddr() net.Addr }).RemoteAddr().String()).
				WithField("parent_conn", tracker.ConnID())
			log.Debugf("[QUIC] [conn=%s] New stream accepted, parent_conn=%s",
				streamTracker.ConnID(), tracker.ConnID())
			select {
			case s.connChan <- &StreamConn{Stream: stream, conn: conn, tracker: streamTracker}:
			case <-s.ctx.Done():
				return
			}

		case _, ok := <-packetBuffer:
			if !ok {
				return
			}
			if !hasPacketHandler {
				select {
				case s.packetChan <- &PacketConn{conn: conn}:
					hasPacketHandler = true
				case <-s.ctx.Done():
					return
				}
			}

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
		Certificates: []tls.Certificate{keyPair},
		NextProtos:   []string{cfg.QUIC.ALPN},
	}

	quicConfig := &quic.Config{
		MaxIdleTimeout:     time.Second * time.Duration(cfg.QUIC.MaxIdleTimeout),
		MaxIncomingStreams: int64(cfg.QUIC.MaxIncomingStreams),
	}

	packetConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(cfg.RemoteHost), Port: cfg.RemotePort})
	if err != nil {
		return nil, common.NewError("QUIC failed to listen UDP").Base(err)
	}

	listener, err := quic.Listen(packetConn, tlsConfig, quicConfig)
	if err != nil {
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
