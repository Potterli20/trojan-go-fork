package dokodemo

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
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
	wg          sync.WaitGroup
}

func (s *Server) dispatchLoop() {
	fixedMetadata := &tunnel.Metadata{
		Address: s.targetAddr,
	}
	buf := make([]byte, MaxPacketSize)
	for {
		n, addr, err := s.udpListener.ReadFrom(buf)
		if err != nil {
			// 同 tls/server.go：不能用 select/default 判断关闭（default 恒就绪会被随机选中）
			if s.ctx.Err() == nil {
				log.Error(common.NewError("dokodemo failed to read from udp socket").Base(err))
			}
			return
		}
		log.Debug("udp packet from", addr)
		toInput := make([]byte, n)
		copy(toInput, buf[:n])
		// 锁内只做查表与建表,解锁后再发送:input 缓冲满时若持锁阻塞发送,
		// 会话读端/超时清理/Close 全部被 mappingLock 卡死
		s.mappingLock.Lock()
		conn, found := s.mapping[addr.String()]
		if !found {
			ctx, cancel := context.WithCancel(s.ctx)
			conn = &PacketConn{
				input:      make(chan []byte, 16),
				output:     make(chan []byte, 16),
				metadata:   fixedMetadata,
				src:        addr,
				PacketConn: s.udpListener,
				ctx:        ctx,
				cancel:     cancel,
			}
			s.mapping[addr.String()] = conn
		}
		s.mappingLock.Unlock()
		// Close() 从不 close channel,旧会话对象与新发送物理隔离,最坏只是丢包
		select {
		case conn.input <- toInput:
		default:
			// 会话可能刚超时清理、已无读者；丢包优于阻塞 dispatch 循环
		}
		if found {
			continue
		}
		select {
		case s.packetChan <- conn:
		case <-s.ctx.Done():
			// 下游已停止消费，退出以免 Close() 的 wg.Wait() 死锁
			conn.Close()
			return
		}

		s.wg.Go(func() {
			timer := time.NewTimer(s.timeout)
			defer timer.Stop()
			// 无论会话因超时还是写失败退出，都必须移除表项并关闭会话：
			// 不删 mapping 则后续同源包持续命中死会话被 select/default 丢弃；
			// 不 Close 则取消会话 ctx，阻塞在 output 发送上的 relay goroutine 永久卡死
			defer func() {
				s.mappingLock.Lock()
				delete(s.mapping, conn.src.String())
				s.mappingLock.Unlock()
				conn.Close()
			}()
			for {
				select {
				case payload := <-conn.output:
					_, err := s.udpListener.WriteTo(payload, conn.src)
					if err != nil {
						log.Error(common.NewError("dokodemo udp write error").Base(err))
						return
					}
					if !timer.Stop() {
						<-timer.C
					}
					timer.Reset(s.timeout)
				case <-s.ctx.Done():
					return
				case <-timer.C:
					log.Debug("closing timeout packetConn")
					return
				}
			}
		})
	}
}

func (s *Server) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	conn, err := s.tcpListener.Accept()
	if err != nil {
		// 监听已关闭（含关闭流程）或出错时不得杀死进程；AcceptConn 调用方会处理 nil/err
		return nil, common.NewError("dokodemo failed to accept connection").Base(err)
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
	// 先关闭监听解除 Accept 阻塞，否则 wg.Wait() 会永久死锁
	s.tcpListener.Close()
	err := s.udpListener.Close()
	s.wg.Wait()
	return err
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
		tcpListener.Close()
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
	server.wg.Go(func() {
		server.dispatchLoop()
	})
	return server, nil
}
