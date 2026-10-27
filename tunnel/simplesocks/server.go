package simplesocks

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/recorder"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/Potterli20/trojan-go-fork/tunnel/mux"
	"github.com/Potterli20/trojan-go-fork/tunnel/trojan"
)

// Server is a simplesocks server
type Server struct {
	underlay   tunnel.Server
	connChan   chan tunnel.Conn
	packetChan chan tunnel.PacketConn
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

func (s *Server) Close() error {
	s.cancel()
	err := s.underlay.Close()
	s.wg.Wait()
	// wg.Wait 之后生产者已退出、不会再向队列投递；这里排空"已完成握手但没被
	// 上层取走"的连接。留在缓冲里的每一条都攥着一个 fd（读过头的 mux 流），
	// 不关就只能等进程退出，反复建栈（api、测试、热重载）时是实打实的堆积。
drain:
	for {
		select {
		case c := <-s.connChan:
			c.Close() //gosec:disable -- 关闭滞留连接，忽略 close 错误
		default:
			break drain
		}
	}
	// wg.Wait 之后生产者已退出、不会再向队列投递；这里排空"已完成握手但没被
	// 上层取走"的连接。留在缓冲里的每一条都攥着一个 fd（UDP 会话），
	// 不关就只能等进程退出，反复建栈（api、测试、热重载）时是实打实的堆积。
drainPacket:
	for {
		select {
		case c := <-s.packetChan:
			c.Close() //gosec:disable -- 关闭滞留连接，忽略 close 错误
		default:
			break drainPacket
		}
	}
	return err
}

// defaultHeaderTimeout 是读取 simplesocks 头的默认时限。
const defaultHeaderTimeout = 10 * time.Second

// headerTimeoutNanos 是读头时限（纳秒），0 表示取默认值。
//
// 为什么不是普通 time.Duration：测试需要把它压到毫秒级，而 acceptLoop 现在是一组
// worker，测试结束、defer 恢复这个值时，某些 worker 可能仍在 readHeader 里读它——
// `go test -race` 实测报 Write/Read 竞争。这类"进程级可调阈值"的读写必须自带同步。
var headerTimeoutNanos atomic.Int64

// headerTimeout 返回当前生效的读头时限。
func headerTimeout() time.Duration {
	if ns := headerTimeoutNanos.Load(); ns > 0 {
		return time.Duration(ns)
	}
	return defaultHeaderTimeout
}

// readHeader 把读头放到独立 goroutine 里，超时或关停时关闭该流。
// 这里不能用 SetDeadline：smux 的 Stream 没有 deadline 方法，而 mux.Conn 上解析到的
// SetDeadline 落在**内嵌的会话 TCP 连接**上——在那儿设时限会波及同一会话的其它流。
// 关闭流则一定奏效：被抛弃的那次 Read 会立即返回错误，所以不留滞留 goroutine。
func (s *Server) readHeader(conn tunnel.Conn) (*tunnel.Metadata, error) {
	type outcome struct {
		metadata *tunnel.Metadata
		err      error
	}
	result := make(chan outcome, 1)
	go func() {
		metadata := new(tunnel.Metadata)
		// 父层 accept 循环那道 panic 边界**够不着这里**：读头是为了"超时"才单起一个
		// goroutine 的，panic 会在这个 goroutine 里冒出来，而全仓没有顶层 recover——
		// 命中一次就是整个进程没了。所以必须在这里就把它变成 error 交回结果通道，
		// 让调用点走既有的"记错误 + 关流 + 继续接纳"分支。
		// 判据 tunnel/simplesocks/accept_panic_test.go 里的那条 panicReadConn。
		err := tunnel.GuardPanic("simplesocks.readHeader", func() error {
			_, err := metadata.ReadFrom(conn)
			return err
		})
		result <- outcome{metadata: metadata, err: err}
	}()

	timer := time.NewTimer(headerTimeout())
	defer timer.Stop()

	select {
	case o := <-result:
		return o.metadata, o.err
	case <-timer.C:
		conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		return nil, common.NewError("timed out waiting for simplesocks header")
	case <-s.ctx.Done():
		conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		return nil, common.NewError("simplesocks server closed")
	}
}

// acceptWorkers 是并发执行"读 simplesocks 头"的协程数。
//
// 读头解析的是**已认证 mux 会话里的对端字节**，而旧写法把 s.readHeader 写在 accept
// 循环体里串行执行：一条被打开却始终不下发字节的流会占满 headerTimeout(10s)，而
// mux/server.go 的 connChan 深度只有 32 且被**所有会话共用**——循环被拖住之后队列填满，
// 每个新流都卡在发送上，全服 mux 流建立速率塌到约 0.1 条/秒，而攻击成本是"每 10 秒
// 开一条静默流"。同型缺陷在 tunnel/adapter（2026-10-20）与 tunnel/trojan（2026-10-27）
// 都已按"循环只做 Accept、per-conn 处理挪进协程"修过。
//
// 并发数固定成 N 而不是"每来一条派一个协程"：那样在途读头的数量就交给对端决定了。
const acceptWorkers = 64

// 下层永久坏掉时，N 个 worker 会各自转成满 CPU 热循环（实测同型问题 400ms 内能读
// 3500 万次），所以退避与日志限流必须跟并行化一起上，口径同 tunnel/socks。
const (
	acceptBackoffStep = 100 * time.Millisecond
	acceptBackoffCap  = 3 * time.Second
)

func (s *Server) acceptLoop() {
	for range acceptWorkers {
		s.wg.Go(s.acceptWorker)
	}
}

func (s *Server) acceptWorker() {
	failures := 0
	for {
		conn, err := tunnel.AcceptConnGuarded(s.underlay, &Tunnel{})
		if err != nil {
			isPanic := errors.Is(err, tunnel.ErrAcceptPanic)
			if s.ctx.Err() != nil {
				return
			}
			failures++
			if failures%10 == 1 {
				if isPanic {
					log.Error(common.NewError("simplesocks accept recovered from a panic in the underlay").Base(err))
				} else {
					log.Error(common.NewError("simplesocks failed to accept connection from underlying tunnel").Base(err))
				}
			} else if log.ShouldLog(log.DebugLevel) {
				log.Debug("simplesocks accept failed (suppressed) x", failures)
			}
			delay := time.Duration(failures) * acceptBackoffStep
			if delay > acceptBackoffCap {
				delay = acceptBackoffCap
			}
			select {
			case <-time.After(delay):
			case <-s.ctx.Done():
				return
			}
			continue
		}
		failures = 0
		// 这条 worker 顺序做完"读头 + 投递"，**不再往下派协程**：本层的慢点是
		// readHeader（不是 AcceptConn）。如果把读头交给派生协程，在途读头数就又回到
		// 由对端决定，上面那个并发上界形同虚设——第一版就犯了这个错，判据
		// TestHeaderReadConcurrencyIsBounded 当场抓到 128 > acceptWorkers。
		// 投递本身带 ctx.Done 分支，关停时 worker 能干净退出，不会拖住 wg.Wait。
		s.serveConn(conn)
	}
}

// serveConn 读头并按命令把这条流交给对应的下游队列（原 acceptLoop 循环体的后半段）。
func (s *Server) serveConn(conn tunnel.Conn) {
	// readHeader 解析的是**对端可控的前几个字节**，panic 边界就在它自己为超时单起的那个
	// goroutine 里（外层这道边界够不着它）；错误带着 ErrAcceptPanic 回到这里，
	// 走下面既有的"记错误 + 关流 + 继续接纳"分支即可。
	metadata, err := s.readHeader(conn)
	if err != nil {
		log.Error(common.NewError("simplesocks server failed to read header").Base(err))
		conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		return
	}
	switch metadata.Command {
	case Connect:
		select {
		case s.connChan <- &Conn{
			metadata: metadata,
			Conn:     conn,
		}:
		case <-s.ctx.Done():
			// 下游已停止消费，关闭连接并退出，否则 Close() 的 wg.Wait() 死锁
			conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			return
		}
		Record(conn, metadata)
	case Associate:
		select {
		case s.packetChan <- &PacketConn{
			Conn: conn,
		}:
		case <-s.ctx.Done():
			conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			return
		}
	default:
		log.Error(common.NewErrorf("simplesocks unknown command %d", metadata.Command))
		conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	}
}

func (s *Server) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	select {
	case conn := <-s.connChan:
		return conn, nil
	case <-s.ctx.Done():
		return nil, common.NewError("simplesocks server closed")
	}
}

func (s *Server) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	select {
	case packetConn := <-s.packetChan:
		return packetConn, nil
	case <-s.ctx.Done():
		return nil, common.NewError("simplesocks server closed")
	}
}

func NewServer(ctx context.Context, underlay tunnel.Server) (*Server, error) {
	ctx, cancel := context.WithCancel(ctx)
	server := &Server{
		underlay:   underlay,
		ctx:        ctx,
		connChan:   make(chan tunnel.Conn, 32),
		packetChan: make(chan tunnel.PacketConn, 32),
		cancel:     cancel,
	}
	server.wg.Go(func() {
		server.acceptLoop()
	})
	log.Debug("simplesocks server created")
	return server, nil
}

func Record(conn tunnel.Conn, metadata *tunnel.Metadata) {
	var userHash string
	if muxConn, ok := conn.(*mux.Conn); ok {
		c := muxConn.Conn
		if trojanConn, ok2 := c.(*trojan.InboundConn); ok2 {
			userHash = trojanConn.Hash()
		}
	}
	if userHash != "" {
		log.Debug("user", userHash, "from", conn.RemoteAddr(), "tunneling to", metadata.Address)
		recorder.Add(userHash, conn.RemoteAddr(), metadata.Address, "TCP", nil)
	}
}
