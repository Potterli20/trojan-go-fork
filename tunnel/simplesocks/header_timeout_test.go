package simplesocks

import (
	"bytes"
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// stalledConn 只在下发头之前就被关掉时才结束 Read——模拟"客户端开了流却不写字节"。
type stalledConn struct {
	read   chan struct{} // 关闭表示 Read 应返回
	closed atomic.Bool
}

func newStalledConn() *stalledConn {
	return &stalledConn{read: make(chan struct{})}
}

func (c *stalledConn) Read(_ []byte) (int, error) {
	<-c.read
	return 0, &net.OpError{Op: "read", Err: net.ErrClosed}
}

func (c *stalledConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *stalledConn) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		close(c.read)
	}
	return nil
}
func (c *stalledConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *stalledConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *stalledConn) SetDeadline(time.Time) error      { return nil }
func (c *stalledConn) SetReadDeadline(time.Time) error  { return nil }
func (c *stalledConn) SetWriteDeadline(time.Time) error { return nil }
func (c *stalledConn) Metadata() *tunnel.Metadata       { return nil }

// headerConn 预先备好一段合法头，Read 从中取。
type headerConn struct {
	*stalledConn
	data *bytes.Reader
}

func newHeaderConn(md *tunnel.Metadata) *headerConn {
	buf := make([]byte, 0, 64)
	w := bytes.NewBuffer(buf)
	if _, err := md.WriteTo(w); err != nil {
		panic(err)
	}
	return &headerConn{stalledConn: newStalledConn(), data: bytes.NewReader(w.Bytes())}
}

func (c *headerConn) Read(p []byte) (int, error) {
	return c.data.Read(p)
}

// fakeUnderlay 是一个只提供固定若干连接的 tunnel.Server。
// Close 必须能解除 AcceptConn 的阻塞（真实实现里是关监听），否则被模拟的
// Server.Close() 的 wg.Wait() 会永远等下去。
type fakeUnderlay struct {
	conns  chan tunnel.Conn
	ctx    context.Context
	cancel context.CancelFunc
}

func newFakeUnderlay(ctx context.Context, buffered int) *fakeUnderlay {
	inner, cancel := context.WithCancel(ctx)
	return &fakeUnderlay{
		conns:  make(chan tunnel.Conn, buffered),
		ctx:    inner,
		cancel: cancel,
	}
}

func (f *fakeUnderlay) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	select {
	case c := <-f.conns:
		return c, nil
	case <-f.ctx.Done():
		return nil, context.Canceled
	}
}

func (f *fakeUnderlay) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	<-f.ctx.Done()
	return nil, context.Canceled
}

func (f *fakeUnderlay) Close() error {
	f.cancel()
	return nil
}

// setHeaderTimeoutForTest 把读头时限压到 d，并在测试结束时恢复。
//
// 恢复走 t.Cleanup 而不是裸 defer：defer 在测试函数返回时执行，而 t.Cleanup 在
// 之后（LIFO）——先关 Server、再恢复默认值，worker 就不会在 Close 之前撞上一个
// 半恢复的包级配置。写入本身走 atomic，即便顺序仍有交错也不构成数据竞争。
func setHeaderTimeoutForTest(t *testing.T, d time.Duration) {
	t.Helper()
	old := headerTimeoutNanos.Swap(int64(d))
	t.Cleanup(func() { headerTimeoutNanos.Store(old) })
}

// TestAcceptLoopIsNotBlockedBySilentStream 盯的是 acceptLoop 的单串行结构：
// 它读头原先没有任何时限，一条被打开却不下发字节的流会把它永久卡住 ——
// 之后所有 mux 会话都建不起来（simplesocks 只在这条链上服务 mux 内的流），
// 而且 Close() 的 wg.Wait() 只能靠 proxy 的 5s 有界兜底返回。
//
// 判据：静默流必须在 headerTimeout 内被关闭并放行，下一个合法会话的头仍要被读到。
func TestAcceptLoopIsNotBlockedBySilentStream(t *testing.T) {
	setHeaderTimeoutForTest(t, 100*time.Millisecond) // 把等待压到测试可接受的范围

	ctx := t.Context()

	silent := newStalledConn()
	addr := &tunnel.Address{AddressType: tunnel.DomainName, DomainName: "after-silent.example.com", Port: 443}
	good := newHeaderConn(&tunnel.Metadata{Command: Connect, Address: addr})

	underlay := newFakeUnderlay(ctx, 2)
	underlay.conns <- silent
	underlay.conns <- good

	server, err := NewServer(ctx, underlay)
	common.Must(err)
	// Close 要有界等待：读头没有时限时，acceptLoop 会卡在静默流上，
	// 而 Close 里的 wg.Wait() 就永远返回不了（真实进程里只是被 proxy 的 5s
	// 兜底遮住）。用有界等待把这个后果也变成可见的失败，而不是让测试挂死。
	t.Cleanup(func() {
		closed := make(chan struct{})
		go func() {
			server.Close() //gosec:disable -- 错误忽略：测试清理
			close(closed)
		}()
		select {
		case <-closed:
		case <-time.After(2 * time.Second):
			t.Error("server.Close() 没在 2s 内返回：acceptLoop 仍卡在无时限的读头上")
		}
	})

	accepted := make(chan *tunnel.Metadata, 1)
	go func() {
		conn, err := server.AcceptConn(&Tunnel{})
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- conn.Metadata()
	}()

	select {
	case md := <-accepted:
		if md == nil {
			t.Fatal("AcceptConn 返回错误：静默流之后没有放行合法会话")
		}
		if md.Address == nil || md.Address.DomainName != "after-silent.example.com" {
			t.Fatalf("放行后的会话地址不对：%v", md.Address)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("acceptLoop 被一条静默流永久卡住：读头没有时限，后续 mux 会话全部无法建立")
	}

	// 读头已经并行了（acceptLoop 是工作池），所以"合法会话被放行"与"静默流被判超时"
	// 之间没有先后关系了：这里必须等，而不是假设它已经发生。
	waitFlag(t, "超时的静默流", func() bool { return silent.closed.Load() }, 3*time.Second)
}

// waitFlag 有界地等到 cond 成立。并行化之后，"某条流被判超时/panic 并被关闭"不再与
// "下一条会话被放行"存在顺序关系，所以这类断言必须自己等，否则就是拿时序巧合当判据。
func waitFlag(t *testing.T, what string, cond func() bool, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("%s 没有在 %v 内被回收：它会继续占着 fd/会话资源", what, within)
	}
}
