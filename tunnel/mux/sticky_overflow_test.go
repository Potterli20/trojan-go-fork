package mux

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// recordingConn 记下每一次写出的字节，用来核对帧头的顺序与完整度。
type recordingConn struct {
	mu      sync.Mutex
	written []byte
}

func (c *recordingConn) Read([]byte) (int, error)         { return 0, nil }
func (c *recordingConn) Close() error                     { return nil }
func (c *recordingConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *recordingConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *recordingConn) SetDeadline(time.Time) error      { return nil }
func (c *recordingConn) SetReadDeadline(time.Time) error  { return nil }
func (c *recordingConn) SetWriteDeadline(time.Time) error { return nil }

func (c *recordingConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// smux 的帧头缓冲区会被复用，照 stickyConn 的规矩先复制再留存
	c.written = append(c.written, append([]byte{}, p...)...)
	return len(p), nil
}

func (c *recordingConn) Metadata() *tunnel.Metadata { return &tunnel.Metadata{} }

func (c *recordingConn) snapshot() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte{}, c.written...)
}

var _ tunnel.Conn = (*recordingConn)(nil)

// TestStickyConnHeaderQueueOverflowDoesNotWedge 盯粘连队列满员的那一刻。
//
// stickyConn.Write 全程持有 writeMu，并把 SYN/FIN 帧头挂进容量 128 的队列，
// 只有下一次数据写（stickToPayload）会把它排空。连续开够 128 条「只建流不写数据」
// 的流时，第 129 次 Write 就在持锁状态下阻塞在 channel 发送上：
//   - 唯一会排空队列的「下一次数据写」由同一个 smux 写循环发出，而它正被这次 Write 堵住；
//   - Close 里的 padding 写要抢同一把 writeMu，于是也卡住。
//
// 于是整条会话永久停滞且关不掉。mux 客户端侧 `concurrency <= 0` 是显式支持的
// 「不设限」配置（见 tunnel/mux/client.go），所以这不是理论状态。
//
// 判据：写满 300 个 SYN 帧头必须返回，且已写出的帧头按顺序连续、与仍在队列里的
// 数量之和正好等于 300。
func TestStickyConnHeaderQueueOverflowDoesNotWedge(t *testing.T) {
	rec := &recordingConn{}
	c := newStickyConn(rec)

	const total = 300
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range total {
			// p[0]=1 是 smux 帧头，p[1]=0 是 cmdSYN，最后一字节当作 streamID 以便核对顺序
			header := []byte{1, 0, 0, 0, 0, 0, 0, byte(i)}
			if _, err := c.Write(header); err != nil {
				t.Errorf("write #%d: %v", i, err)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("队列满员后 Write 持 writeMu 阻塞，Close 也再拿不到锁——整条会话卡死")
	}

	queued := len(c.synQueue)
	got := rec.snapshot()
	if len(got)%8 != 0 {
		t.Fatalf("写出的字节数不是帧头整数倍: %d", len(got))
	}
	flushed := len(got) / 8
	if flushed+queued != total {
		t.Fatalf("帧头丢了: 已写出 %d + 队列 %d != %d", flushed, queued, total)
	}
	if flushed == 0 {
		t.Fatal("队列满员时没有就地发出任何帧头，判据未覆盖到溢出分支")
	}
	for i := range flushed {
		if got[i*8+1] != 0 {
			t.Fatalf("帧头 %d 不是 SYN", i)
		}
		if got[i*8+7] != byte(i) {
			t.Fatalf("帧头顺序错乱: 第 %d 个 streamID=%d", i, got[i*8+7])
		}
	}
}
