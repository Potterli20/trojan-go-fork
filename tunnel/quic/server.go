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
	xrayCongestion "github.com/xtls/xray-core/transport/internet/hysteria/congestion"
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
	if s.congestion == "" {
		s.congestion = "bbr"
	}

	switch s.congestion {
	case "brutal":
		if s.brutalUp > 0 && s.brutalDown > 0 {
			xrayCongestion.UseBrutal(conn, min(s.brutalUp, s.brutalDown))
			log.Debug("QUIC brutal congestion control enabled on server with speed:", min(s.brutalUp, s.brutalDown), "bps")
		} else {
			log.Warn("Brutal congestion control requires both brutal_up and brutal_down to be set")
		}
	case "force-brutal":
		if s.brutalUp > 0 {
			xrayCongestion.UseBrutal(conn, s.brutalUp)
			log.Debug("QUIC force-brutal congestion control enabled on server with speed:", s.brutalUp, "bps")
		} else {
			log.Warn("Force-brutal congestion control requires brutal_up to be set")
		}
	case "bbr":
		xrayCongestion.UseBBR(conn, "standard")
		log.Debug("QUIC BBR congestion control enabled on server")
	case "reno":
		log.Debug("QUIC Reno congestion control enabled on server")
	default:
		log.Warn("Unknown congestion control:", s.congestion, ", using default")
	}
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

		s.activeConns.Store(quicConn.RemoteAddr().String(), conn)
		log.Debug("QUIC connection accepted from", quicConn.RemoteAddr())

		s.wg.Go(func() {
			s.handleConnection(conn)
		})
	}
}

func (s *Server) handleConnection(conn any) {
	defer func() {
		conn.(interface {
			CloseWithError(code uint32, reason string) error
		}).CloseWithError(0, "connection closed")
		s.activeConns.Delete(conn.(interface{ RemoteAddr() net.Addr }).RemoteAddr().String())
		log.Debug("QUIC connection closed from", conn.(interface{ RemoteAddr() net.Addr }).RemoteAddr())
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
			log.Debug("QUIC stream accepted from", conn.(interface{ RemoteAddr() net.Addr }).RemoteAddr())
			s.connChan <- &StreamConn{Stream: stream, conn: conn}

		case _, ok := <-packetBuffer:
			if !ok {
				return
			}
			if !hasPacketHandler {
				s.packetChan <- &PacketConn{conn: conn}
				hasPacketHandler = true
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
