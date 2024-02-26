package dokodemo

import (
	"context"
	"net"
	"sync"
	"time"

	"gitlab.atcatw.org/atca/community-edition/trojan-go/common"
	"gitlab.atcatw.org/atca/community-edition/trojan-go/config"
	"gitlab.atcatw.org/atca/community-edition/trojan-go/log"
	"gitlab.atcatw.org/atca/community-edition/trojan-go/tunnel"
)

type Server struct {
	tunnel.Server
	tcpListener net.Listener
	udpListener net.PacketConn
	packetChan  chan tunnel.PacketConn
	timeout     time.Duration
	targetAddr  *tunnel.Address
	mappingLock sync.Mutex
	mapping     map[string]*PacketConn
	ctx         context.Context
	cancel      context.CancelFunc
}

func (s *Server) dispatchLoop() {
	fixedMetadata := &tunnel.Metadata{
		Address: s.targetAddr,
	}
	for {
		buf := make([]byte, MaxPacketSize)
		n, addr, err := s.udpListener.ReadFrom(buf)
		if err != nil {
			select {
			case <-s.ctx.Done():
			default:
				log.Fatal(common.NewError("dokodemo failed to read from udp socket").Base(err))
			}
			return
		}
		log.Debug("udp packet from", addr)
		s.mappingLock.Lock()
		if conn, found := s.mapping[addr.String()]; found {
			toInput := make([]byte, n)
			copy(toInput, buf)
			conn.input <- toInput
			s.mappingLock.Unlock()
			continue
		}
		ctx, cancel := context.WithCancel(s.ctx)
		conn := &PacketConn{
			input:      make(chan []byte, 16),
			output:     make(chan []byte, 16),
			metadata:   fixedMetadata,
			src:        addr,
			PacketConn: s.udpListener,
			ctx:        ctx,
			cancel:     cancel,
		}
		s.mapping[addr.String()] = conn
		s.mappingLock.Unlock()
		toInput := make([]byte, n)
		copy(toInput, buf)
		conn.input <- toInput
		s.packetChan <- conn

		go func(conn *PacketConn) {
			for {
				select {
				case payload := <-conn.output:
					// "Multiple goroutines may invoke methods on a Conn simultaneously."
					_, err := s.udpListener.WriteTo(payload, conn.src)
					if err != nil {
						log.Error(common.NewError("dokodemo udp write error").Base(err))
						return
					}
				case <-s.ctx.Done():
					return
				case <-time.After(s.timeout):
					s.mappingLock.Lock()
					delete(s.mapping, conn.src.String())
					s.mappingLock.Unlock()
					conn.Close()
					log.Debug("closing timeout packetConn")
					return
				}
			}
		}(conn)
	}
}

func (s *Server) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	conn, err := s.tcpListener.Accept()
	if err != nil {
		log.Fatal(common.NewError("dokodemo failed to accept connection").Base(err))
	}
	return &Conn{
		Conn: conn,
		targetMetadata: &tunnel.Metadata{
			Address: s.targetAddr,
		},
	}, nil
}

func (s *Server) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	select {
	case conn := <-s.packetChan:
		return conn, nil
	case <-s.ctx.Done():
		return nil, common.NewError("dokodemo server closed")
	}
}

func (s *Server) Close() error {
	s.cancel()
	s.tcpListener.Close()
	s.udpListener.Close()
	return nil
}

func NewServer(ctx context.Context, _ tunnel.Server) (*Server, error) {
	cfg := config.FromContext(ctx, Name).(*Config)
	targetAddr := tunnel.NewAddressFromHostPort("tcp", cfg.TargetHost, cfg.TargetPort)
	listenAddr := tunnel.NewAddressFromHostPort("tcp", cfg.LocalHost, cfg.LocalPort)

	tcpListener, err := net.Listen("tcp", listenAddr.String())
	if err != nil {
		return nil, common.NewError("failed to listen tcp").Base(err)
	}
	udpListener, err := net.ListenPacket("udp", listenAddr.String())
	if err != nil {
		return nil, common.NewError("failed to listen udp").Base(err)
	}

	ctx, cancel := context.WithCancel(ctx)
	server := &Server{
		tcpListener: tcpListener,
		udpListener: udpListener,
		targetAddr:  targetAddr,
		mapping:     make(map[string]*PacketConn),
		packetChan:  make(chan tunnel.PacketConn, 32),
		timeout:     time.Second * time.Duration(cfg.UDPTimeout),
		ctx:         ctx,
		cancel:      cancel,
	}
	go server.dispatchLoop()
	return server, nil
}
