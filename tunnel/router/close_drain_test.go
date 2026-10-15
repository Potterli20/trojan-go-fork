package router

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// quietUnderlay 提供 net.PacketConn 的最小实现：不产生包，只在 Close 后被唤醒。
type quietUnderlay struct{ done chan struct{} }

func (c *quietUnderlay) ReadFrom([]byte) (int, net.Addr, error) {
	<-c.done
	return 0, nil, io.EOF
}

func (c *quietUnderlay) WriteTo(p []byte, _ net.Addr) (int, error) { return len(p), nil }
func (c *quietUnderlay) LocalAddr() net.Addr                       { return &net.UDPAddr{} }
func (c *quietUnderlay) RemoteAddr() net.Addr                      { return &net.UDPAddr{} }
func (c *quietUnderlay) SetDeadline(time.Time) error               { return nil }
func (c *quietUnderlay) SetReadDeadline(time.Time) error           { return nil }
func (c *quietUnderlay) SetWriteDeadline(time.Time) error          { return nil }
func (c *quietUnderlay) Close() error                              { return nil }

// quietPacketConn 是 proxy 侧夹具：不跑 packetLoop，所以 Close 里的 wg.Wait 会立刻
// 返回，队列状态完全由测试掌握。
type quietPacketConn struct {
	quietUnderlay
}

func (c *quietPacketConn) ReadWithMetadata([]byte) (int, *tunnel.Metadata, error) {
	<-c.done
	return 0, nil, io.EOF
}

func (c *quietPacketConn) WriteWithMetadata(p []byte, _ *tunnel.Metadata) (int, error) {
	return len(p), nil
}

func newQuietConn(t *testing.T) *PacketConn {
	t.Helper()
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &PacketConn{
		proxy:      &quietPacketConn{done: done},
		PacketConn: &quietUnderlay{done: done},
		packetChan: make(chan *packetInfo, 16),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// countNewBufs 把 packetBufPool.New 换成可计数的版本，返回新增分配次数计数器。
// 判据靠它：池被取空后，任何一次 Get 只要走了 New 就说明有 buffer 没能归还。
func countNewBufs(t *testing.T) *atomic.Int64 {
	t.Helper()
	orig := packetBufPool.New
	var created atomic.Int64
	packetBufPool.New = func() any {
		created.Add(1)
		buf := make([]byte, MaxPacketSize)
		return &buf
	}
	t.Cleanup(func() { packetBufPool.New = orig })
	return &created
}

// drainBufPool 把池取空并把取走的 buffer 全部扣在 held 里（不让它们回到池），
// 直到连续两次 Get 都走 New，据此确认池已彻底空（含其它 P 的窃取路径）。
func drainBufPool(t *testing.T, created *atomic.Int64) {
	t.Helper()
	var held [][]byte
	for emptyStreak := 0; emptyStreak < 2; {
		before := created.Load()
		held = append(held, getPacketBuf())
		if created.Load() > before {
			emptyStreak++
		} else {
			emptyStreak = 0
		}
		if len(held) > 4096 {
			t.Fatal("取不空 packetBufPool：可能有 buffer 被永久扣在某个持有者手里")
		}
	}
	t.Cleanup(func() {
		// 归还测试扣下的 buffer，避免污染同包内其它用例的池状态
		for _, b := range held {
			putPacketBuf(b)
		}
	})
	created.Store(0)
}

// TestCloseReturnsQueuedPacketBufs 钉住会话拆除时的池外滞留。
//
// 两个 packetLoop 都遵守「从池借一个 buffer → 随 packetInfo 入队 → 由消费端 copy
// 完再归还」。问题在拆除路径：Close 只 cancel + 关两端 + wg.Wait，队列里压着的
// 那几个 *packetInfo 连同它们各自借出的 8KB buffer 一起变成垃圾——它们不会泄露
// 堆内存，但会永久离开池，等于每拆一个会话就把池抽空一截。池容量按在途包数走，
// 会话一多就得持续重新 make(8KB)，分配次数回到与包数同阶。
func TestCloseReturnsQueuedPacketBufs(t *testing.T) {
	created := countNewBufs(t)
	drainBufPool(t, created)

	conn := newQuietConn(t)
	const queued = 3
	for range queued {
		buf := getPacketBuf()
		conn.packetChan <- &packetInfo{
			src:     &tunnel.Metadata{Address: &tunnel.Address{AddressType: tunnel.IPv4, IP: net.IPv4(1, 2, 3, 4).To4(), Port: 53}},
			payload: buf[:16],
		}
	}
	created.Store(0)

	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Close 之后应当能不加新分配地把这 3 个 buffer 依次取回，且取回的是全长
	for i := range queued {
		if buf := getPacketBuf(); len(buf) != MaxPacketSize || cap(buf) != MaxPacketSize {
			t.Fatalf("第 %d 个 buffer 归还尺寸不对：len=%d cap=%d", i, len(buf), cap(buf))
		}
	}
	if n := created.Load(); n != 0 {
		t.Fatalf("Close 没有把队列里 %d 个在途 buffer 归还进池：拆除会话后仍新建了 %d 个 8KB buffer", queued, n)
	}
}
