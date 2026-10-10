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

// TestAcceptLoopIsNotBlockedBySilentStream 盯的是 acceptLoop 的单串行结构：
// 它读头原先没有任何时限，一条被打开却不下发字节的流会把它永久卡住 ——
// 之后所有 mux 会话都建不起来（simplesocks 只在这条链上服务 mux 内的流），
// 而且 Close() 的 wg.Wait() 只能靠 proxy 的 5s 有界兜底返回。
//
// 判据：静默流必须在 headerTimeout 内被关闭并放行，下一个合法会话的头仍要被读到。
func TestAcceptLoopIsNotBlockedBySilentStream(t *testing.T) {
	defer func(old time.Duration) { headerTimeout = old }(headerTimeout)
	headerTimeout = 100 * time.Millisecond // 把等待压到测试可接受的范围

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

	if !silent.closed.Load() {
		t.Fatal("超时的静默流没有被关闭——它会在 headerTimeout 之后继续占着资源")
	}
}
