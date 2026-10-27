package simplesocks

import (
	"sync/atomic"
	"testing"
	"time"
)

// countingStalledConn 在每次进入 Read 时发一个信号，用来观测"同时在读头的流数"。
type countingStalledConn struct {
	*stalledConn
	entered chan struct{}
}

func newCountingStalledConn() *countingStalledConn {
	return &countingStalledConn{
		stalledConn: newStalledConn(),
		entered:     make(chan struct{}, 4),
	}
}

func (c *countingStalledConn) Read(p []byte) (int, error) {
	select {
	case c.entered <- struct{}{}:
	default:
	}
	return c.stalledConn.Read(p)
}

// 两条静默流必须**同时**处在读头中，而不是第一条占住 accept 循环 headerTimeout(10s)、
// 第二条排队。
//
// 旧写法把 s.readHeader 写在循环体里串行执行：一条被打开却不写字节的流就拖住整个
// simplesocks 入口，而 mux/server.go 的 connChan 深度只有 32 且被所有会话共用——队列
// 填满后每个新流都卡在发送上，全服 mux 流建立速率塌到约 0.1 条/秒，攻击成本只是
// "每 10 秒开一条静默流"。既有那条 TestAcceptLoopIsNotBlockedBySilentStream 把时限压到
// 100ms，钉的是"有界"，钉不住"并行/吞吐"，所以这条不重叠。
func TestHeaderReadsAreNotSerialized(t *testing.T) {
	// 真实量级：让"反正 headerTimeout 之后会自愈"这条在水准线内不可用，
	// 测到的就是并行本身。
	setHeaderTimeoutForTest(t, 30*time.Second)

	ctx := t.Context()
	s1, s2 := newCountingStalledConn(), newCountingStalledConn()
	underlay := newFakeUnderlay(ctx, 2)
	underlay.conns <- s1
	underlay.conns <- s2

	server, err := NewServer(ctx, underlay)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() {
		s1.Close() //gosec:disable -- 测试清理
		s2.Close() //gosec:disable -- 测试清理
		server.Close()
	})

	// NewServer 已经起了 acceptLoop（内部是 acceptWorkers 个 worker），这里不再另起一份。
	if !waitEnter(t, s1.entered, 2*time.Second) {
		t.Fatal("前置条件不成立：第一条流根本没被读头")
	}
	if !waitEnter(t, s2.entered, 2*time.Second) {
		t.Fatal("第二条流没有在第一条仍被读头时开始读：accept 循环仍是串行的，" +
			"一条静默流就能让整个 mux 入口停摆 headerTimeout")
	}
}

func waitEnter(t *testing.T, ch chan struct{}, within time.Duration) bool {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case <-ch:
			return true
		case <-deadline:
			return false
		}
	}
}

// 上界也要钉住：在途读头的数量不能由对端决定。
func TestHeaderReadConcurrencyIsBounded(t *testing.T) {
	setHeaderTimeoutForTest(t, 30*time.Second)

	ctx := t.Context()
	underlay := newFakeUnderlay(ctx, acceptWorkers*2)
	conns := make([]*countingStalledConn, 0, acceptWorkers*2)
	for range acceptWorkers * 2 {
		c := newCountingStalledConn()
		underlay.conns <- c
		conns = append(conns, c)
	}

	server, err := NewServer(ctx, underlay)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() {
		for _, c := range conns {
			c.Close() //gosec:disable -- 测试清理
		}
		server.Close()
	})

	// 给足时间让所有可用的 worker 都领到一条流
	time.Sleep(300 * time.Millisecond)
	var inRead int32
	for _, c := range conns {
		select {
		case <-c.entered:
			atomic.AddInt32(&inRead, 1)
		default:
		}
	}
	if int(inRead) > acceptWorkers {
		t.Fatalf("%d 条流同时在读头，超过 acceptWorkers=%d：并发数交给了对端决定", inRead, acceptWorkers)
	}
	if int(inRead) < 2 {
		t.Fatalf("只有 %d 条流进入读头：并行化没生效", inRead)
	}
}
