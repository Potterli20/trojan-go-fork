package socks

import (
	"bytes"
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

const (
	Connect   tunnel.Command = 1
	Associate tunnel.Command = 3
)

const (
	MaxPacketSize = 1024 * 8
)

// handshakeTimeout 限定 socks 握手(version/method/command/address)整体时限
const handshakeTimeout = 30 * time.Second

type Server struct {
	connChan         chan tunnel.Conn
	packetChan       chan tunnel.PacketConn
	underlay         tunnel.Server
	localHost        string
	localPort        int
	timeout          time.Duration
	listenPacketConn tunnel.PacketConn
	mapping          map[string]*PacketConn
	mappingLock      sync.RWMutex
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup
}

func (s *Server) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	select {
	case conn := <-s.connChan:
		return conn, nil
	case <-s.ctx.Done():
		return nil, common.NewError("socks server closed")
	}
}

func (s *Server) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	select {
	case conn := <-s.packetChan:
		return conn, nil
	case <-s.ctx.Done():
		return nil, common.NewError("socks server closed")
	}
}

func (s *Server) acceptConnLoop() {
	for {
		conn, err := s.underlay.AcceptConn(&Tunnel{})
		if err != nil {
			// 不能用 "select ctx.Done / default" 判断关闭：ctx 恰已取消时两分支随机命中；
			// 直接检查 ctx.Err()
			if s.ctx.Err() != nil {
				log.Debug("exiting")
				return
			}
			log.Error(common.NewError("socks failed to accept conn").Base(err))
			continue
		}
		s.wg.Go(func() {
			// 握手期间对端可能静默(如端口扫描);截止时间覆盖 version/method/
			// command/address 全部握手读,移交下游前解除
			conn.SetDeadline(time.Now().Add(handshakeTimeout))
			handledConn, err := s.handshake(conn)
			if err != nil {
				log.Error(common.NewError("socks failed to handshake").Base(err))
				conn.Close() // handshake 失败时确保关闭原始连接
				return
			}
			// 握手成功的连接所有权移交给上层（connChan 的使用方负责关闭），
			// 此处不能 defer Close，否则连接在移交给 connChan 后会被立即关闭
			switch handledConn.Metadata().Command {
			case Connect:
				log.Info("socks connect request from", handledConn.RemoteAddr(), "metadata", handledConn.Metadata())
				err = s.connect(handledConn)
				if err != nil {
					log.Error(common.NewError("socks failed to respond connect").Base(err))
					handledConn.Close()
					return
				}
				// 连接即将移交下游长期使用,必须解除握手期截止时间
				conn.SetDeadline(time.Time{})
				select {
				case s.connChan <- handledConn:
				case <-s.ctx.Done():
					log.Debug("exiting")
					handledConn.Close()
				}
			case Associate:
				log.Info("socks associate request from", handledConn.RemoteAddr(), "metadata", handledConn.Metadata())
				err = s.associate(handledConn, handledConn.Metadata().Address)
				if err != nil {
					log.Error(common.NewError("socks failed to respond associate").Base(err))
					handledConn.Close()
					return
				}
				// associate 的 TCP 连接响应后即被放弃,无需解除截止时间
			default:
				log.Error("socks unknown command", handledConn.Metadata().Command)
				handledConn.Close()
			}
		})
	}
}

func (s *Server) Close() error {
	s.cancel()
	s.listenPacketConn.Close()
	err := s.underlay.Close()
	s.wg.Wait()
	return err
}

func (s *Server) handshake(conn net.Conn) (*Conn, error) {
	version := [1]byte{}
	if _, err := conn.Read(version[:]); err != nil {
		return nil, common.NewError("failed to read socks version").Base(err)
	}
	if version[0] != 5 {
		return nil, common.NewErrorf("invalid socks version %d", version[0])
	}
	nmethods := [1]byte{}
	if _, err := conn.Read(nmethods[:]); err != nil {
		return nil, common.NewError("failed to read NMETHODS")
	}
	if _, err := io.CopyN(io.Discard, conn, int64(nmethods[0])); err != nil {
		return nil, common.NewError("socks failed to read methods").Base(err)
	}
	if _, err := conn.Write([]byte{0x5, 0x0}); err != nil {
		return nil, common.NewError("failed to respond auth").Base(err)
	}

	buf := [3]byte{}
	if _, err := conn.Read(buf[:]); err != nil {
		return nil, common.NewError("failed to read command")
	}

	addr := new(tunnel.Address)
	_, err := addr.ReadFrom(conn)
	if err != nil {
		return nil, err
	}

	return &Conn{
		metadata: &tunnel.Metadata{
			Command: tunnel.Command(buf[1]),
			Address: addr,
		},
		Conn: conn,
	}, nil
}

func (s *Server) connect(conn net.Conn) error {
	_, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	return err
}

func (s *Server) associate(conn net.Conn, addr *tunnel.Address) error {
	// RFC 1928：UDP ASSOCIATE 响应中的 BND.ADDR/BND.PORT 是服务端 UDP 中继地址，
	// 客户端随后向该地址发送 UDP 报文。标准 socks5 客户端（如 txthinking/socks5）
	// 会原样使用该地址，回显请求的 DST（通常为 0.0.0.0:0）会导致 UDP 关联永远无法建立。
	udpAddr, ok := s.listenPacketConn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return common.NewError("socks failed to get udp relay address")
	}
	relayAddr := tunnel.NewAddressFromHostPort("udp", udpAddr.IP.String(), udpAddr.Port)
	buf := bytes.NewBuffer([]byte{0x05, 0x00, 0x00})
	_, err := relayAddr.WriteTo(buf)
	common.Must(err)
	_, err = conn.Write(buf.Bytes())
	return err
}

func (s *Server) packetDispatchLoop() {
	buf := make([]byte, MaxPacketSize)
	for {
		n, src, err := s.listenPacketConn.ReadFrom(buf)
		if err != nil {
			// 不能用 "select ctx.Done / default" 判断关闭：ctx 恰已取消时两分支随机命中，
			// 可能误走 continue 变成 busy loop；直接检查 ctx.Err()
			if s.ctx.Err() != nil {
				log.Debug("exiting")
				return
			}
			continue
		}
		log.Debug("socks recv udp packet from", src)
		s.mappingLock.RLock()
		conn, found := s.mapping[src.String()]
		s.mappingLock.RUnlock()
		if !found {
			ctx, cancel := context.WithCancel(s.ctx)
			conn = &PacketConn{
				input:      make(chan *packetInfo, 128),
				output:     make(chan *packetInfo, 128),
				ctx:        ctx,
				cancel:     cancel,
				PacketConn: s.listenPacketConn,
				src:        src,
			}
			s.wg.Go(func() {
				defer conn.Close()
				// UDP 会话空闲超时与 dokodemo/tproxy 对齐,取配置的 UDPTimeout(默认 60s)
				timeout := s.timeout
				timer := time.NewTimer(timeout)
				defer timer.Stop()
				responseBuf := make([]byte, MaxPacketSize)
				responseBuffer := bytes.NewBuffer(responseBuf[:0])
				for {
					select {
					case info := <-conn.output:
						responseBuffer.Reset()
						responseBuffer.Write([]byte{0, 0, 0}) // RSV, FRAG
						_, err := info.metadata.Address.WriteTo(responseBuffer)
						if err != nil {
							log.Error("socks failed to write address")
							return
						}
						responseBuffer.Write(info.payload)
						_, err = s.listenPacketConn.WriteTo(responseBuffer.Bytes(), conn.src)
						if err != nil {
							log.Error("socks failed to respond packet to", conn.src)
							return
						}
						log.Debug("socks respond udp packet to", conn.src, "metadata", info.metadata)
						if !timer.Stop() {
							<-timer.C
						}
						timer.Reset(timeout)
					case <-timer.C:
						log.Info("socks udp session timeout, closed")
						s.mappingLock.Lock()
						delete(s.mapping, conn.src.String())
						s.mappingLock.Unlock()
						return
					case <-conn.ctx.Done():
						s.mappingLock.Lock()
						delete(s.mapping, conn.src.String())
						s.mappingLock.Unlock()
						log.Info("socks udp session closed")
						return
					}
				}
			})

			s.mappingLock.Lock()
			s.mapping[src.String()] = conn
			s.mappingLock.Unlock()

			select {
			case s.packetChan <- conn:
				log.Info("socks new udp session from", src)
			case <-s.ctx.Done():
				// 下游已停止消费，必须退出，否则关闭时该 goroutine 永久阻塞导致泄露
				conn.Close()
				log.Debug("exiting")
				return
			}
		}
		r := bytes.NewReader(buf[3:n])
		address := new(tunnel.Address)
		_, err = address.ReadFrom(r)
		if err != nil {
			log.Error(common.NewError("socks failed to parse incoming packet").Base(err))
			continue
		}
		remaining := r.Len()
		if remaining <= 0 {
			continue
		}
		payload := make([]byte, remaining)
		_, _ = r.Read(payload)
		select {
		case conn.input <- &packetInfo{
			metadata: &tunnel.Metadata{
				Address: address,
			},
			payload: payload,
		}:
		default:
		}
	}
}

func NewServer(ctx context.Context, underlay tunnel.Server) (*Server, error) {
	cfg := config.FromContext(ctx, Name).(*Config)
	ctx, cancel := context.WithCancel(ctx)
	listenPacketConn, err := underlay.AcceptPacket(nil)
	if err != nil {
		cancel()
		return nil, common.NewError("socks failed to accept packet conn").Base(err)
	}
	server := &Server{
		underlay:         underlay,
		localHost:        cfg.LocalHost,
		localPort:        cfg.LocalPort,
		timeout:          time.Duration(cfg.UDPTimeout) * time.Second,
		connChan:         make(chan tunnel.Conn, 32),
		packetChan:       make(chan tunnel.PacketConn, 32),
		mapping:          make(map[string]*PacketConn),
		ctx:              ctx,
		cancel:           cancel,
		listenPacketConn: listenPacketConn,
	}
	server.wg.Go(func() {
		server.acceptConnLoop()
	})
	server.wg.Go(func() {
		server.packetDispatchLoop()
	})
	return server, nil
}
