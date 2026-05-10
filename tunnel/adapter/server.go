package adapter

import (
	"context"
	"net"
	"sync"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/Potterli20/trojan-go-fork/tunnel/freedom"
	"github.com/Potterli20/trojan-go-fork/tunnel/http"
	"github.com/Potterli20/trojan-go-fork/tunnel/socks"
)

type Server struct {
	tcpListener net.Listener
	udpListener net.PacketConn
	socksConn   chan tunnel.Conn
	httpConn    chan tunnel.Conn
	socksLock   sync.RWMutex
	nextSocks   bool
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

func (s *Server) acceptConnLoop() {
	for {
		conn, err := s.tcpListener.Accept()
		if err != nil {
			select {
			case <-s.ctx.Done():
				log.Debug("exiting")
				return
			default:
				continue
			}
		}
		rewindConn := common.NewRewindConn(conn)
		rewindConn.SetBufferSize(16)
		buf := [3]byte{}
		_, err = rewindConn.Read(buf[:])
		rewindConn.Rewind()
		rewindConn.StopBuffering()
		if err != nil {
			log.Error(common.NewError("failed to detect proxy protocol type").Base(err))
			rewindConn.Close()
			continue
		}
		s.socksLock.RLock()
		isSocks := buf[0] == 5 && s.nextSocks
		s.socksLock.RUnlock()

		freedomConn := &freedom.Conn{
			Conn: rewindConn,
		}

		if isSocks {
			log.Debug("socks5 connection")
			select {
			case s.socksConn <- freedomConn:
			case <-s.ctx.Done():
				freedomConn.Close()
				log.Debug("exiting")
				return
			}
		} else {
			log.Debug("http connection")
			select {
			case s.httpConn <- freedomConn:
			case <-s.ctx.Done():
				freedomConn.Close()
				log.Debug("exiting")
				return
			}
		}
	}
}

func (s *Server) AcceptConn(overlay tunnel.Tunnel) (tunnel.Conn, error) {
	if _, ok := overlay.(*http.Tunnel); ok {
		select {
		case conn := <-s.httpConn:
			return conn, nil
		case <-s.ctx.Done():
			return nil, common.NewError("adapter closed")
		}
	} else if _, ok := overlay.(*socks.Tunnel); ok {
		s.socksLock.Lock()
		s.nextSocks = true
		s.socksLock.Unlock()
		select {
		case conn := <-s.socksConn:
			return conn, nil
		case <-s.ctx.Done():
			return nil, common.NewError("adapter closed")
		}
	} else {
		panic("invalid overlay")
	}
}

func (s *Server) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return &freedom.PacketConn{
		UDPConn: s.udpListener.(*net.UDPConn),
	}, nil
}

func (s *Server) Close() error {
	s.cancel()
	s.wg.Wait()
	s.tcpListener.Close()
	return s.udpListener.Close()
}

func NewServer(ctx context.Context, _ tunnel.Server) (*Server, error) {
	cfg := config.FromContext(ctx, Name).(*Config)
	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(ctx)

	addr := tunnel.NewAddressFromHostPort("tcp", cfg.LocalHost, cfg.LocalPort)
	tcpListener, err := net.Listen("tcp", addr.String())
	if err != nil {
		cancel()
		return nil, common.NewError("adapter failed to create tcp listener").Base(err)
	}
	udpListener, err := net.ListenPacket("udp", addr.String())
	if err != nil {
		cancel()
		return nil, common.NewError("adapter failed to create tcp listener").Base(err)
	}
	server := &Server{
		tcpListener: tcpListener,
		udpListener: udpListener,
		socksConn:   make(chan tunnel.Conn, 32),
		httpConn:    make(chan tunnel.Conn, 32),
		ctx:         ctx,
		cancel:      cancel,
	}
	log.Info("adapter listening on tcp/udp:", addr)
	server.wg.Add(1)
	go func() {
		defer server.wg.Done()
		server.acceptConnLoop()
	}()
	return server, nil
}
