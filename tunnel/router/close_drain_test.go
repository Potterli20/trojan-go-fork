package router

import (
	"context"
	"io"
	"net"
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

// TestCloseDrainsQueuedPacketInfos 钉住会话拆除时的池外滞留。
//
// 两个 packetLoop 都遵守「从池借一个 buffer → 随 packetInfo 入队 → 由消费端 copy
// 完再归还」。问题在拆除路径：Close 只 cancel + 关两端 + wg.Wait，队列里压着的
// 那几个 *packetInfo 连同它们各自借出的 8KB buffer 一起变成垃圾——堆内存能回收，
// 但池被抽空一截，会话一多就得持续重新 make(8KB)。
//
// 判据刻意只看"队列被排空了"和"归还的切片是全长的"这两件确定性的事。
// 早先的版本数的是「Close 后连着 Get 三次，一次都不该走 New」，在
// `make test-race`（-race -tags full）下会假红：sync.Pool 的本地队列会在 P 之间被
// 窃取，取回哪个对象、要不要新建都不由测试决定——实测同一份正确代码下新建了 2 个。
// 那是拿调度器当判据，不是拿代码当判据。
func TestCloseDrainsQueuedPacketInfos(t *testing.T) {
	conn := newQuietConn(t)
	const queued = 3
	for range queued {
		buf := getPacketBuf()
		conn.packetChan <- &packetInfo{
			src:     &tunnel.Metadata{Address: &tunnel.Address{AddressType: tunnel.IPv4, IP: net.IPv4(1, 2, 3, 4).To4(), Port: 53}},
			payload: buf[:16],
		}
	}
	if len(conn.packetChan) != queued {
		t.Fatalf("前置条件不成立：队列里 %d 项，期望 %d", len(conn.packetChan), queued)
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 拆除之后队列必须是空的：压着的每一项都握着一个池化 buffer 的借用权
	if n := len(conn.packetChan); n != 0 {
		t.Fatalf("Close 之后 packetChan 仍压着 %d 项：这些 buffer 永久离开池，每拆一条会话抽空一截", n)
	}

	// 归还时必须还原成全长，否则下一个使用者拿到的是被 append 削短过的切片
	for i := range queued + 1 {
		buf := getPacketBuf()
		if len(buf) != MaxPacketSize || cap(buf) != MaxPacketSize {
			t.Fatalf("第 %d 次取到的 buffer 尺寸不对：len=%d cap=%d（期望都是 %d）",
				i, len(buf), cap(buf), MaxPacketSize)
		}
		putPacketBuf(buf)
	}
}
