package adapter

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/Potterli20/trojan-go-fork/tunnel/freedom"
	"github.com/Potterli20/trojan-go-fork/tunnel/http"
	"github.com/Potterli20/trojan-go-fork/tunnel/socks"
)

// firstByteTimeout 限定协议嗅探阶段等待对端数据的时限
const firstByteTimeout = 10 * time.Second

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
			// 不能用 "select ctx.Done / default" 判断关闭：ctx 恰已取消时两分支随机命中，
			// 可能误走 continue 变成 busy loop；直接检查 ctx.Err()
			if s.ctx.Err() != nil {
				log.Debug("exiting")
				return
			}
			continue
		}
		rewindConn := common.NewRewindConn(conn)
		rewindConn.SetBufferSize(16)
		// 对端静默时不设截止时间会让 accept 循环永久阻塞，Close() 的 wg.Wait() 随之挂起
		rewindConn.SetReadDeadline(time.Now().Add(firstByteTimeout))
		buf := [3]byte{}
		_, err = rewindConn.Read(buf[:])
		rewindConn.Rewind()
		rewindConn.StopBuffering()
		rewindConn.SetReadDeadline(time.Time{})
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
	// 先关闭监听解除 acceptConnLoop 的 Accept 阻塞，否则 wg.Wait() 会永久死锁
	s.tcpListener.Close()
	err := s.udpListener.Close()
	s.wg.Wait()
	return err
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
		tcpListener.Close()
		cancel()
		return nil, common.NewError("adapter failed to create udp listener").Base(err)
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
	server.wg.Go(func() {
		server.acceptConnLoop()
	})
	return server, nil
}
