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

// udpSessionQueueSize 是每个 UDP 会话收发队列的深度，与 dokodemo/socks 的 16 对齐。
// 旧的 128 配上最大 8KB 的负载，两条通道就是每会话 2MB 的驻留上限，而 mapping
// 只按空闲超时清理。队列满时投递侧走 select/default 丢包：UDP 语义下丢包优于
// 按会话无界占内存。
const udpSessionQueueSize = 16

// tproxyPacketInfo 是 UDP 读取协程交给 dispatch 主循环的一条报文。
// 原先它是 packetDispatchLoop 里的函数内类型 + 局部 channel；提到包级并挂到 Server 上，
// 是为了让 dispatch 的会话逻辑能在**没有 CAP_NET_ADMIN** 的环境里被测——
// ReadFromUDP 需要 IP_RECVORIGDSTADDR cmsg，而那要求监听 socket 带 IP_TRANSPARENT，
// 普通 CI（含本仓库的 ubuntu-24.04）根本构造不出真实入口。测试直接往 packetQueue
// 投递伪造的 info，跳开 socket 层，被验证的仍是同一份会话代码。
type tproxyPacketInfo struct {
	src     *net.UDPAddr
	dst     *net.UDPAddr
	payload []byte
}

type Server struct {
	tcpListener net.Listener
	udpListener *net.UDPConn
	packetQueue chan *tproxyPacketInfo
	packetChan  chan tunnel.PacketConn
	timeout     time.Duration
	mappingLock sync.RWMutex
	mapping     map[string]*PacketConn
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	// 监听只能关一次：ctx 观察者、Close、以及 UDP 连续读错误的自杀路径都会关。
	closeOnce sync.Once
	closeErr  error
}

// closeListeners 幂等地关掉 TCP/UDP 监听。三个调用方：Close、NewServer 装的 ctx
// 观察者、以及 UDP 连续读错误时的自我关停。
func (s *Server) closeListeners() error {
	s.closeOnce.Do(func() {
		tcpErr := s.tcpListener.Close()
		udpErr := s.udpListener.Close()
		if tcpErr != nil {
			s.closeErr = tcpErr
		} else {
			s.closeErr = udpErr
		}
	})
	return s.closeErr
}

func (s *Server) Close() error {
	s.cancel()
	// 先关闭监听解除 Accept 阻塞，否则 wg.Wait() 会永久死锁
	err := s.closeListeners()
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
	inbound, err := s.decorate(conn)
	if err != nil {
		// 唯一的失败回收点：Accept 出来的 socket 已经归本层所有，任何后续失败都必须
		// 关掉它。relay 拿到错误只会记一条日志继续 Accept，没人回收就是每失败一条
		// 泄露一个 fd，最终 EMFILE 把进程拖死。
		conn.Close() //gosec:disable -- 错误忽略：失败回收路径，错误无处可报
		return nil, err
	}
	return inbound, nil
}

// decorate 给已经 Accept 出来的连接补上原始目的地址。
// 刻意不做裸类型断言：以前的 conn.(*net.TCPConn) 一旦监听器交出的不是 *net.TCPConn,
// 就会在 relay 的 accept 循环里 panic，等于整个进程被打崩。
func (s *Server) decorate(conn net.Conn) (tunnel.Conn, error) {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return nil, common.NewError("tproxy listener accepted a non-TCP connection")
	}
	dst, err := getOriginalTCPDest(tcpConn)
	if err != nil {
		return nil, common.NewError("tproxy failed to obtain original address of tcp socket").Base(err)
	}
	// dst 就是 GetOriginalDst 解出来的 *net.TCPAddr，每条连接一次也不必绕字符串
	address, err := tunnel.NewAddressFromNetAddr("tcp", dst)
	if err != nil {
		return nil, common.NewError("tproxy failed to parse original address").Base(err)
	}
	log.Info("tproxy connection from", conn.RemoteAddr().String(), "metadata", dst.String())
	return &Conn{
		metadata: &tunnel.Metadata{
			Address: address,
		},
		Conn: conn,
	}, nil
}

func (s *Server) packetDispatchLoop() {
	s.wg.Go(func() {
		buf := make([]byte, MaxPacketSize)
		readErrors := 0
		for {
			n, src, dst, err := ReadFromUDP(s.udpListener, buf)
			if err != nil {
				if s.ctx.Err() != nil {
					return
				}
				readErrors++
				log.Error(common.NewError("tproxy failed to read from udp socket").Base(err))
				if readErrors >= 10 {
					log.Error("tproxy udp socket persistently failing, closing server")
					// 这里绝不能用 s.Close()：本 goroutine 是 s.wg 的成员，而 Close
					// 里有 s.wg.Wait() —— 等于等自己退出，永久自锁死，且之后外部真正
					// 的 Close 也会卡在同一个 Wait。只拆掉监听与 ctx，让其它成员各自
					// 从 ctx.Done / 监听错误里退出，Wait 交给外部的 Close 去做。
					s.cancel()
					s.closeListeners() //gosec:disable -- 错误忽略：故障自关停路径
					return
				}
				// 错误退避：在 ctx 取消时立即返回，否则等待 100ms 后重试。
				select {
				case <-s.ctx.Done():
					return
				case <-time.After(time.Millisecond * 100):
				}
				continue
			}
			readErrors = 0
			log.Debug("udp packet from", src, "metadata", dst, "size", n)
			payload := make([]byte, n)
			copy(payload, buf[:n])
			select {
			case s.packetQueue <- &tproxyPacketInfo{
				src:     src,
				dst:     dst,
				payload: payload,
			}:
			case <-s.ctx.Done():
				return
			}
		}
	})

	for {
		var info *tproxyPacketInfo
		select {
		case info = <-s.packetQueue:
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
				input:      make(chan *packetInfo, udpSessionQueueSize),
				output:     make(chan *packetInfo, udpSessionQueueSize),
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
				conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
				return
			}

			sessionConn := conn
			s.wg.Go(func() {
				defer sessionConn.Close()
				defer func() {
					s.mappingLock.Lock()
					delete(s.mapping, sessionConn.src.String())
					s.mappingLock.Unlock()
				}()
				log.Debug("udp packet daemon for", sessionConn.src.String())
				timer := time.NewTimer(s.timeout)
				defer timer.Stop()
				for {
					select {
					case info := <-sessionConn.output:
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
							sessionConn.src.(*net.UDPAddr),
						)
						if err != nil {
							log.Error(common.NewError("failed to dial tproxy udp").Base(err))
							return
						}
						n, err := back.Write(info.payload)
						if err != nil {
							back.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
							log.Error(common.NewError("tproxy udp write error").Base(err))
							return
						}
						log.Debug("recv packet, send back to", sessionConn.src, "payload", len(info.payload), "sent", n)
						back.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
						if !timer.Stop() {
							<-timer.C
						}
						timer.Reset(s.timeout)
					case <-s.ctx.Done():
						log.Debug("exiting")
						return
					case <-sessionConn.ctx.Done():
						// 下游 relay 拆会话走的是 conn.Close()，只 cancel 会话自己的 ctx。
						// 不监听它就得等空闲 timer 过期（最长 UDPTimeout）；这期间 mapping
						// 挂着死表项，同源地址的包全投进没人读的 input 丢掉，而且 found
						// 恒真所以再也投递不出新会话——同一源端口重开会话就是黑洞。
						// socks/dokodemo 的同型 daemon 都有这条，nat 模式的 TPROXY 之前漏了。
						log.Debug("tproxy udp session closed by consumer")
						return
					case <-timer.C:
						log.Debug("packet session ", sessionConn.src.String(), "timeout")
						return
					}
				}
			})
		}

		// 手里已经有 *net.UDPAddr，别拼成字符串再解析一遍：这条是**每 UDP 包**一次的
		// 路径，旧写法每包多付一次 IP.String() 格式化 + 一次地址解析（口径同 6857b3f2）
		dstAddr, addrErr := tunnel.NewAddressFromNetAddr("udp", info.dst)
		if addrErr != nil {
			log.Error("tproxy failed to build destination address", addrErr)
			continue
		}

		select {
		case conn.input <- &packetInfo{
			metadata: &tunnel.Metadata{
				Address: dstAddr,
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
		// tcpListener 此刻已经 bind 住端口，只 cancel 的话它没有任何持有者会去关：
		// 每次失败建栈漏一个 fd + 一个被占死的端口（api/测试这类在库模式反复建栈的
		// 场景会逐次累计）。dokodemo/adapter 的失败路径都显式关了 TCP listener，这里对齐。
		tcpListener.Close() //gosec:disable -- 错误忽略：失败路径回收，真实错误由返回值给出
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
		packetQueue: make(chan *tproxyPacketInfo, 1024),
	}
	server.wg.Go(func() {
		server.packetDispatchLoop()
	})
	// 取消 ctx 就要能解除 AcceptConn 里阻塞的 Accept：nat 模式的 source 就是 TPROXY，
	// 而 proxy 的关停顺序是 cancel -> wg.Wait -> releaseTunnels，缺了这个观察者
	// 每次停机都要烧满 proxy.go 的 5s 有界兜底。
	server.wg.Go(func() {
		<-server.ctx.Done()
		server.closeListeners() //gosec:disable -- 错误忽略：由 Close 汇总
	})
	log.Info("tproxy server listening on", tcpListener.Addr(), "(tcp)", udpListener.LocalAddr(), "(udp)")
	log.Debug("tproxy server created")
	return server, nil
}
