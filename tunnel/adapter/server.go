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

// acceptBackoffStep / acceptBackoffCap 是 accept 出错后的退避参数（线性、封顶 3s），
// 与 tunnel/trojan、tunnel/simplesocks 的同名常量同参。
const (
	acceptBackoffStep = 100 * time.Millisecond
	acceptBackoffCap  = 3 * time.Second
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
	// closeOnce/closeErr：client 模式里 socks 与 http 两个端点共用同一个
	// adapter 实例（proxy/client/client.go:61），而 releaseTunnels 会逐个关
	// source，于是 Close 必然被调用两次。第二次 udpListener.Close() 返回
	// "use of closed network connection"，一路冒到 main 变成 FATAL 与非零退出码。
	// 与 tunnel/transport/server.go 同型问题，照同一套路子：整个关闭只跑一次。
	closeOnce sync.Once
	closeErr  error
}

func (s *Server) acceptConnLoop() {
	// acceptErrors 只用于退避与日志限流。监听一旦**持续**报错（fd 耗尽，或监听被外部
	// 关掉而 s.ctx 没取消），原来的裸 continue 就是一条满 CPU 的热循环，而且一行日志
	// 都没有——比崩掉更难发现。socks 的同型循环实测 400ms 内读了 3500 万次，
	// 这里照同一个口径补退避 + 限流。
	acceptErrors := 0
	for {
		conn, err := s.tcpListener.Accept()
		if err != nil {
			// 不能用 "select ctx.Done / default" 判断关闭：ctx 恰已取消时两分支随机命中，
			// 可能误走 continue 变成 busy loop；直接检查 ctx.Err()
			if s.ctx.Err() != nil {
				log.Debug("exiting")
				return
			}
			acceptErrors++
			if acceptErrors%10 == 1 {
				log.Error(common.NewError("tcp adapter persistently failing to accept").Base(err))
			} else if log.ShouldLog(log.DebugLevel) {
				log.Debug("tcp adapter accept failed (suppressed) x", acceptErrors)
			}
			// 线性退避、封顶 3s，与 tunnel/trojan、tunnel/simplesocks 的 acceptWorker 同参：
			// 监听永久坏掉时裸 continue 就是满 CPU，而每条都记 Error 又把日志变成攻击面。
			delay := min(time.Duration(acceptErrors)*acceptBackoffStep, acceptBackoffCap)
			select {
			case <-s.ctx.Done():
				log.Debug("exiting")
				return
			case <-time.After(delay):
			}
			continue
		}
		acceptErrors = 0
		// 嗅探必须放独立 goroutine：它要等对端发够首字节，而 Accept 是串行的。
		// 旧写法把 Read 直接写在循环体里，对端只连接不发数据就能让整个 socks/http
		// 入口停止接纳新连接，最长 firstByteTimeout(10s)——一个慢速扫描器就能做到。
		s.wg.Go(func() {
			s.sniffConn(conn)
		})
	}
}

// sniffConn 读走前几个字节判断协议，再把连接投递给对应的下游。
func (s *Server) sniffConn(conn net.Conn) {
	rewindConn := common.NewRewindConn(conn)
	rewindConn.SetBufferSize(16)
	// 对端静默时不设截止时间会让这条嗅探永久阻塞，Close() 的 wg.Wait() 随之挂起
	rewindConn.SetReadDeadline(time.Now().Add(firstByteTimeout)) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	buf := [3]byte{}
	_, err := rewindConn.Read(buf[:])
	rewindConn.Rewind()
	rewindConn.StopBuffering()
	rewindConn.SetReadDeadline(time.Time{}) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	if err != nil {
		log.Error(common.NewError("failed to detect proxy protocol type").Base(err))
		rewindConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		return
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
			freedomConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			log.Debug("exiting")
			return
		}
	} else {
		log.Debug("http connection")
		select {
		case s.httpConn <- freedomConn:
		case <-s.ctx.Done():
			freedomConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			log.Debug("exiting")
			return
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
	// 不能直接交出 freedom.PacketConn：它内嵌 *net.UDPConn，Close 会把这个**共享**监听
	// 一起关掉。而这个 socket 的所有权属于 Server，只应由 Server.Close 关一次。
	// 交出可关的包装后，proxy 的包转发环在会话结束时 defer inbound.Close() 就替 Server
	// 关了监听，随后 releaseTunnels 调用 adapter.Close 拿到
	// "use of closed network connection"，一路冒到 main 变成 FATAL + 非零退出码
	// —— client 模式带 UDP 的正常关停必然踩到。
	return borrowedPacketConn{
		PacketConn: &freedom.PacketConn{UDPConn: s.udpListener.(*net.UDPConn)},
	}, nil
}

// borrowedPacketConn 借来的包句柄：读写照常转给底层，Close 是空操作。
type borrowedPacketConn struct {
	tunnel.PacketConn
}

func (borrowedPacketConn) Close() error { return nil }

func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = s.close()
	})
	return s.closeErr
}

func (s *Server) close() error {
	s.cancel()
	// 先关闭监听解除 acceptConnLoop 的 Accept 阻塞，否则 wg.Wait() 会永久死锁
	s.tcpListener.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	err := s.udpListener.Close()
	s.wg.Wait()
	// wg.Wait 之后生产者已退出、不会再向队列投递；这里排空"已经嗅完协议、但上层还
	// 没 AcceptConn 取走"的连接。每条都攥着一根已经 Accept 出来的客户端 TCP 连接，
	// 不关就只能等进程退出，反复建栈（api、测试、热重载）时是实打实的堆积。
drain:
	for {
		select {
		case c := <-s.socksConn:
			c.Close() //gosec:disable -- 关闭滞留连接，忽略 close 错误
		default:
			break drain
		}
	}
	// 同上，http 那条队列同样攥着客户端连接。
drainHTTP:
	for {
		select {
		case c := <-s.httpConn:
			c.Close() //gosec:disable -- 关闭滞留连接，忽略 close 错误
		default:
			break drainHTTP
		}
	}
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
		tcpListener.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
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
