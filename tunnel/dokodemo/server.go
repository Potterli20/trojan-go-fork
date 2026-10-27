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

// maxUDPSessions 是会话表条目数的硬上限。mapping 只有空闲超时，条目数等于
// UDPTimeout × 新源地址到达速率，而源地址是可以伪造的；每条会话攥着两条深度 16 的
// 通道、一个 ctx 和一个守护协程。口径与 socks/tproxy 一致（tproxy 2026-10-16 先补，
// 判据见 tunnel/tproxy/session_bound_test.go）。
const maxUDPSessions = 4096

// udpDefaultTimeout 是 UDP 会话空闲超时的兜底值，与配置创建器的默认值同源。
// 非正数必须落回它：time.NewTimer(0) 立即就绪，会话守护协程首轮 select 必走 timer 分支，
// 于是每条 UDP 会话在建立的那一刻就被删表 + Close —— UDP 静默全废，日志只有 Info 一行。
// 同型的 mux idle-timeout 缺陷已按"非正数落文档默认值"修过，这里是另外三个姊妹入站。
const udpDefaultTimeout = 60 * time.Second

func resolveUDPTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return udpDefaultTimeout
	}
	return time.Duration(seconds) * time.Second
}

type Server struct {
	tunnel.Server
	tcpListener net.Listener
	udpListener net.PacketConn
	packetChan  chan tunnel.PacketConn
	timeout     time.Duration
	targetAddr  *tunnel.Address
	mappingLock sync.Mutex
	mapping     map[tunnel.SessionKey]*PacketConn
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	// 监听只能被关一次：ctx 观察者与 Close 都会调用 closeListeners。
	closeOnce sync.Once
	closeErr  error
}

func (s *Server) dispatchLoop() {
	fixedMetadata := &tunnel.Metadata{
		Address: s.targetAddr,
	}
	buf := make([]byte, MaxPacketSize)
	limitDrops := 0 // 会话上限丢弃计数，只用于日志限流
	readErrors := 0 // 连续读错误计数，同样只用于日志限流
	for {
		n, addr, err := s.udpListener.ReadFrom(buf)
		if err != nil {
			// 同 tls/server.go：不能用 select/default 判断关闭（default 恒就绪会被随机选中）
			if s.ctx.Err() != nil {
				return
			}
			// 不能直接 return：一次瞬时读错误（部分平台会把 ICMP 相关错误冒到
			// PacketConn 上）就会让 dokodemo 的 UDP 转发永久停摆，直到进程重启。
			// 退避 100ms 后重试，日志按 10 次限流（口径同 socks/tproxy 的同型循环）。
			readErrors++
			if readErrors%10 == 0 {
				log.Error(common.NewError("dokodemo failed to read from udp socket").Base(err))
			} else if log.ShouldLog(log.DebugLevel) {
				log.Debug(common.NewError("dokodemo udp read error").Base(err))
			}
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		readErrors = 0
		log.Debug("udp packet from", addr)
		// 从池里借一块装这个包的负载；借用权随 input 队列交给会话，最后由
		// ReadWithMetadata 归还，或会话被拆时由 Close 的排空归还。
		toInput := getPacketBuf()
		copy(toInput[:n], buf[:n])
		toInput = toInput[:n]
		// 锁内只做查表与建表,解锁后再发送:input 缓冲满时若持锁阻塞发送,
		// 会话读端/超时清理/Close 全部被 mappingLock 卡死
		// 键归一放在 tunnel.SessionKey：语义与 addr.String() 一致（v4-mapped-v6 归一、
		// zone 保留），但这条是**每包一次**的路径，不必再格式化字符串。
		addrKey := tunnel.NewSessionKey(addr)
		s.mappingLock.Lock()
		conn, found := s.mapping[addrKey]
		if !found {
			if len(s.mapping) >= maxUDPSessions {
				s.mappingLock.Unlock()
				// 这一包直接丢掉：toInput 还没交给任何会话，也没有半建的会话对象要回收
				// （闸卡在造对象之前）。借出的负载就地还回去。
				putPacketBuf(toInput)
				// 限流：到上限之后这条路径的速率就等于**新源地址到达速率**，而源地址可
				// 伪造；addr.String() 也必须算在 ShouldLog 之后，否则默认级别下每丢一包
				// 照样付一次格式化 + 整条脱敏链（口径同 tunnel/socks 与 AGENTS.md）。
				limitDrops++
				if limitDrops%10 == 0 {
					log.Error("dokodemo udp session limit reached, dropping packet from", addr.String())
				} else if log.ShouldLog(log.DebugLevel) {
					log.Debug("dokodemo udp session limit reached, dropping packet from", addr.String())
				}
				continue
			}
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
			s.mapping[addrKey] = conn
		}
		// 投递放在解锁之前、与 daemon 的“删表项 + Close 排空”互斥：daemon 的 defer
		// 先 delete（持写锁）再 conn.Close()（排空 input）。dispatch 若在解锁后才投递，
		// 可能落在“已 delete、排空也跑完”之后——那一包会成功入队却再无读者，借出的
		// 负载永久不归还。锁内投递用 select/default 非阻塞，不会把会话读端/超时清理/
		// Close 卡在 mappingLock 上（旧注释担心的正是阻塞发送）。
		select {
		case conn.input <- toInput:
		default:
			// 会话可能刚超时清理、已无读者；丢包优于阻塞 dispatch 循环。
			// 丢包的同时必须把借出的负载还回去，否则每次投递失败漏一块 8KB。
			putPacketBuf(toInput)
		}
		s.mappingLock.Unlock()
		if found {
			continue
		}
		select {
		case s.packetChan <- conn:
		case <-s.ctx.Done():
			// 下游已停止消费，退出以免 Close() 的 wg.Wait() 死锁。
			// 这条会话的守护协程从没起来，删表只能由这里做：否则 mapping 里
			// 永久留一条已 Close 的死表项，Close 之后 Server 对象仍被它攥着。
			s.mappingLock.Lock()
			delete(s.mapping, addrKey)
			s.mappingLock.Unlock()
			conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
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
				delete(s.mapping, tunnel.NewSessionKey(conn.src))
				s.mappingLock.Unlock()
				conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			}()
			for {
				select {
				case payload := <-conn.output:
					_, err := s.udpListener.WriteTo(payload, conn.src)
					putPacketBuf(payload)
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
				case <-conn.ctx.Done():
					// 下游 relay 拆会话走的是 conn.Close()，只 cancel 会话自己的 ctx。
					// 不监听它就只能等空闲 timer 过期，最长 UDPTimeout；这期间 mapping
					// 里挂着死表项，同源地址的包全投进没人读的 input 丢掉，且因为
					// found 恒真再也投递不出新会话——同一个源端口重开会话就是黑洞。
					// socks 的同型 daemon 一直有这条，这里属于漏改。
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

// closeListeners 幂等地关掉 TCP/UDP 监听并返回首次关闭的错误。
// 两个调用方：NewServer 装的 ctx 观察者，以及 Close。
func (s *Server) closeListeners() error {
	s.closeOnce.Do(func() {
		// 先 TCP 再 UDP：任一失败都记录，不因一个失败而漏掉另一个
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
		tcpListener.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		return nil, common.NewError("failed to listen udp").Base(err)
	}

	ctx, cancel := context.WithCancel(ctx)
	server := &Server{
		tcpListener: tcpListener,
		udpListener: udpListener,
		targetAddr:  targetAddr,
		mapping:     make(map[tunnel.SessionKey]*PacketConn),
		packetChan:  make(chan tunnel.PacketConn, 32),
		timeout:     resolveUDPTimeout(cfg.UDPTimeout),
		ctx:         ctx,
		cancel:      cancel,
	}
	server.wg.Go(func() {
		server.dispatchLoop()
	})
	// 取消 ctx 就要能解除 AcceptConn 里阻塞的 Accept。proxy 的关停顺序是
	// p.cancel() -> wg.Wait() -> releaseTunnels()，而只有关掉监听才能叫醒裸 Accept；
	// 缺了这个观察者，forward 模式（source 就是 DOKODEMO）每次停机都要等满
	// proxy.go 的 5s 有界兜底，还会顺带把真实关停错误一起埋掉。
	server.wg.Go(func() {
		<-server.ctx.Done()
		server.closeListeners() //gosec:disable -- 错误忽略：由 Close 汇总
	})
	return server, nil
}
