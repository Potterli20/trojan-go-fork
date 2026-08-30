//go:build linux

package tproxy

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

const MaxPacketSize = 1024 * 8

type Server struct {
	tcpListener net.Listener
	udpListener *net.UDPConn
	packetChan  chan tunnel.PacketConn
	timeout     time.Duration
	mappingLock sync.RWMutex
	mapping     map[string]*PacketConn
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

func (s *Server) Close() error {
	s.cancel()
	// 先关闭监听解除 Accept 阻塞，否则 wg.Wait() 会永久死锁
	s.tcpListener.Close()
	err := s.udpListener.Close()
	s.wg.Wait()
	return err
}

func (s *Server) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	conn, err := s.tcpListener.Accept()
	if err != nil {
		// 同 tls/server.go：select/default 判断关闭会被随机选中 default，必须直接检查 ctx
		if s.ctx.Err() == nil {
			log.Error(common.NewError("tproxy failed to accept connection").Base(err))
		}
		return nil, common.NewError("tproxy failed to accept conn")
	}
	dst, err := getOriginalTCPDest(conn.(*net.TCPConn))
	if err != nil {
		return nil, common.NewError("tproxy failed to obtain original address of tcp socket").Base(err)
	}
	address, err := tunnel.NewAddressFromAddr("tcp", dst.String())
	common.Must(err)
	log.Info("tproxy connection from", conn.RemoteAddr().String(), "metadata", dst.String())
	return &Conn{
		metadata: &tunnel.Metadata{
			Address: address,
		},
		Conn: conn,
	}, nil
}

func (s *Server) packetDispatchLoop() {
	type tproxyPacketInfo struct {
		src     *net.UDPAddr
		dst     *net.UDPAddr
		payload []byte
	}
	packetQueue := make(chan *tproxyPacketInfo, 1024)

	go func() {
		buf := make([]byte, MaxPacketSize)
		readErrors := 0
		for {
			n, src, dst, err := ReadFromUDP(s.udpListener, buf)
			if err != nil {
				// 单次读失败(如一个畸形 IPv6 包)不应立刻杀死整个 tproxy 服务；
				// 但 socket 永久损坏或主动关闭时也不能无限空转，连续失败后才 Close
				if s.ctx.Err() != nil {
					return
				}
				readErrors++
				log.Error(common.NewError("tproxy failed to read from udp socket").Base(err))
				if readErrors >= 10 {
					log.Error("tproxy udp socket persistently failing, closing server")
					s.Close()
					return
				}
				time.Sleep(time.Millisecond * 100)
				continue
			}
			readErrors = 0
			log.Debug("udp packet from", src, "metadata", dst, "size", n)
			payload := make([]byte, n)
			copy(payload, buf[:n])
			select {
			case packetQueue <- &tproxyPacketInfo{
				src:     src,
				dst:     dst,
				payload: payload,
			}:
			case <-s.ctx.Done():
				// 关闭时消费端已停止，必须退出，否则 goroutine 永久阻塞在 channel 发送上
				return
			}
		}
	}()

	for {
		var info *tproxyPacketInfo
		select {
		case info = <-packetQueue:
		case <-s.ctx.Done():
			log.Debug("exiting")
			return
		}

		s.mappingLock.RLock()
		conn, found := s.mapping[info.src.String()]
		s.mappingLock.RUnlock()

		if !found {
			ctx, cancel := context.WithCancel(s.ctx)
			conn = &PacketConn{
				input:      make(chan *packetInfo, 128),
				output:     make(chan *packetInfo, 128),
				PacketConn: s.udpListener,
				ctx:        ctx,
				cancel:     cancel,
				src:        info.src,
			}

			s.mappingLock.Lock()
			s.mapping[info.src.String()] = conn
			s.mappingLock.Unlock()

			log.Info("new tproxy udp session from", info.src.String(), "metadata", info.dst.String())
			select {
			case s.packetChan <- conn:
			case <-s.ctx.Done():
				// 下游已停止消费，关闭会话并退出，否则 Close() 的 wg.Wait() 死锁
				conn.Close()
				return
			}

			go func(conn *PacketConn) {
				defer conn.Close()
				// 无论会话因超时、写失败还是拨号失败退出，都必须从 mapping 移除，
				// 否则后续同源包持续命中死会话被 select/default 静默丢弃，
				// 该客户端的 UDP 永久失效，死表项也随之累积
				defer func() {
					s.mappingLock.Lock()
					delete(s.mapping, conn.src.String())
					s.mappingLock.Unlock()
				}()
				log.Debug("udp packet daemon for", conn.src.String())
				timer := time.NewTimer(s.timeout)
				defer timer.Stop()
				for {
					select {
					case info := <-conn.output:
						if info.metadata.AddressType != tunnel.IPv4 &&
							info.metadata.AddressType != tunnel.IPv6 {
							log.Error("tproxy invalid response metadata address", info.metadata)
							continue
						}
						back, err := DialUDP(
							"udp",
							&net.UDPAddr{
								IP:   info.metadata.IP,
								Port: info.metadata.Port,
							},
							conn.src.(*net.UDPAddr),
						)
						if err != nil {
							log.Error(common.NewError("failed to dial tproxy udp").Base(err))
							return
						}
						n, err := back.Write(info.payload)
						if err != nil {
							back.Close()
							log.Error(common.NewError("tproxy udp write error").Base(err))
							return
						}
						log.Debug("recv packet, send back to", conn.src, "payload", len(info.payload), "sent", n)
						back.Close()
						if !timer.Stop() {
							<-timer.C
						}
						timer.Reset(s.timeout)
					case <-s.ctx.Done():
						log.Debug("exiting")
						return
					case <-timer.C:
						log.Debug("packet session ", conn.src.String(), "timeout")
						return
					}
				}
			}(conn)
		}

		select {
		case conn.input <- &packetInfo{
			metadata: &tunnel.Metadata{
				Address: tunnel.NewAddressFromHostPort("udp", info.dst.IP.String(), info.dst.Port),
			},
			payload: info.payload,
		}:
		default:
			// 会话可能刚超时清理、已无读者；丢包优于阻塞整个 dispatch 循环
		}
		log.Debug("tproxy packet sent with metadata", info.dst, "size", len(info.payload))
	}
}

func (s *Server) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	select {
	case conn := <-s.packetChan:
		log.Info("tproxy packet conn accepted")
		return conn, nil
	case <-s.ctx.Done():
		return nil, io.EOF
	}
}

func NewServer(ctx context.Context, _ tunnel.Server) (*Server, error) {
	cfg := config.FromContext(ctx, Name).(*Config)
	ctx, cancel := context.WithCancel(ctx)
	listenAddr := tunnel.NewAddressFromHostPort("tcp", cfg.LocalHost, cfg.LocalPort)
	ip, err := listenAddr.ResolveIP()
	if err != nil {
		cancel()
		return nil, common.NewError("invalid tproxy local address").Base(err)
	}
	tcpListener, err := ListenTCP("tcp", &net.TCPAddr{
		IP:   ip,
		Port: cfg.LocalPort,
	})
	if err != nil {
		cancel()
		return nil, common.NewError("tproxy failed to listen tcp").Base(err)
	}

	udpListener, err := ListenUDP("udp", &net.UDPAddr{
		IP:   ip,
		Port: cfg.LocalPort,
	})
	if err != nil {
		cancel()
		return nil, common.NewError("tproxy failed to listen udp").Base(err)
	}

	server := &Server{
		tcpListener: tcpListener,
		udpListener: udpListener,
		ctx:         ctx,
		cancel:      cancel,
		timeout:     time.Duration(cfg.UDPTimeout) * time.Second,
		mapping:     make(map[string]*PacketConn),
		packetChan:  make(chan tunnel.PacketConn, 32),
	}
	server.wg.Go(func() {
		server.packetDispatchLoop()
	})
	log.Info("tproxy server listening on", tcpListener.Addr(), "(tcp)", udpListener.LocalAddr(), "(udp)")
	log.Debug("tproxy server created")
	return server, nil
}
