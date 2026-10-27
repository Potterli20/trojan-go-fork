package trojan

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// 只在 AcceptConn 上做文章的假下层：把每次调用挂到 release 上，用来数"同时在途的握手"。
// 串行循环的观测值恒为 1；工作池应当是 2..acceptWorkers。
type parkedAcceptServer struct {
	tunnel.Server

	mu       sync.Mutex
	calls    int
	inflight int
	maxSeen  int
	release  chan struct{}
	errAfter error
	errFirst bool
}

func (s *parkedAcceptServer) AcceptConn(_ tunnel.Tunnel) (tunnel.Conn, error) {
	s.mu.Lock()
	s.calls++
	s.inflight++
	if s.inflight > s.maxSeen {
		s.maxSeen = s.inflight
	}
	first := s.calls == 1
	s.mu.Unlock()

	if s.errFirst && first {
		return nil, s.errAfter
	}
	if s.release != nil {
		<-s.release
	}
	s.mu.Lock()
	s.inflight--
	s.mu.Unlock()
	return nil, s.errAfter
}

func (s *parkedAcceptServer) snapshot() (calls, maxSeen int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, s.maxSeen
}

func newTestTrojanServer(underlay tunnel.Server) (*Server, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		underlay:   underlay,
		connChan:   make(chan tunnel.Conn, 32),
		muxChan:    make(chan tunnel.Conn, 32),
		packetChan: make(chan tunnel.PacketConn, 32),
		ctx:        ctx,
		cancel:     cancel,
	}
	return s, cancel
}

// 一条握手还没结束，就必须已经有第二个 worker 在等下一条连接。
//
// 旧写法把 `s.underlay.AcceptConn(&Tunnel{})` 写在 accept 循环体里：下层那次调用里跑着
// tls 握手、websocket 的 http.ReadRequest + Accept、shadowsocks 的密文探测，全都在等
// **未认证对端**发字节。所以一条只完成 TCP/TLS 连接却不发数据的对端，就能让整个入站
// 停摆最长 30s；每 30s 补一条静默连接，合法用户就再也连不上。同型缺陷在 tunnel/adapter
// （2026-10-20，判据 sniff_parallel_test.go）已修过，trojan 是漏网的姊妹。
func TestAcceptWorkersDoNotSerializeHandshakes(t *testing.T) {
	under := &parkedAcceptServer{release: make(chan struct{}), errAfter: errors.New("release")}
	s, cancel := newTestTrojanServer(under)
	defer cancel()

	// 按生产形态启动（NewServer 里就是 s.wg.Go(s.acceptLoop)）。直接在测试协程里调用
	// 会让"仍然是串行"的实现把测试卡死在调用处，只能靠 go test 超时变红——那不是判据。
	s.wg.Go(s.acceptLoop)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, maxSeen := under.snapshot(); maxSeen >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, maxSeen := under.snapshot()
	if maxSeen < 2 {
		t.Fatalf("同时在途的握手只有 %d：AcceptConn 仍被串行执行，一条静默对端就能让整个入站停摆", maxSeen)
	}

	// 上界同样要钉住：并发数不能由对端决定（每 Accept 一次就派一个协程的写法会在这里红）。
	calls, _ := under.snapshot()
	if calls > acceptWorkers {
		t.Fatalf("已经有 %d 次 Accept 调用，超过 acceptWorkers=%d：在途握手数没被钉死", calls, acceptWorkers)
	}

	// 放行：parked 的 worker 各自返回错误后进入退避，cancel 让它们干净退出
	close(under.release)
	cancel()
	s.wg.Wait()
}

// 持久错误不能变成热循环：下层一回来就报错时，N 个 worker 各自必须退避。
// 用 synctest 的假时钟跑满几个退避周期，不占墙钟。
func TestAcceptWorkerBacksOffOnPersistentError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		under := &parkedAcceptServer{errAfter: errors.New("underlay is gone"), errFirst: false}
		s, cancel := newTestTrojanServer(under)
		defer cancel()

		s.acceptLoop()
		synctest.Sleep(1500 * time.Millisecond)
		cancel()

		calls, maxSeen := under.snapshot()
		// 线性退避 100ms/200ms/300ms…，1.5s 内每个 worker 最多约 5 次；
		// 没有退避的话这里是"多少取决于 CPU 多快"，实测同型问题 400ms 能到千万次。
		if ceiling := acceptWorkers * 8; calls > ceiling {
			t.Fatalf("1.5s 假时钟内发生了 %d 次 Accept（上限 %d）：出错分支没有退避，会变成满 CPU 热循环",
				calls, ceiling)
		}
		if maxSeen > acceptWorkers {
			t.Fatalf("同时在途 %d 个 Accept，超过 acceptWorkers=%d", maxSeen, acceptWorkers)
		}
		s.wg.Wait()
	})
}
