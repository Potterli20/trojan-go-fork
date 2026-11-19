package http

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

const connectRequest = "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"

// blockingWriteConn 是一个可控的裸连接：
//   - Read 先把预置的 CONNECT 请求交出去，之后返回 EOF（让 handler 走完首请求读取）；
//   - Write（CONNECT 分支写 200 响应那一下）阻塞在 writeGate 上，直到 Close 或 gate 被放行；
//   - Close 会被 watcher 调用，记录次数并放行 gate，让阻塞的 Write 带着错误返回。
//
// 这样"对端零窗口导致 Write 永久阻塞"就被钉成一个确定的通道同步，不靠真实 socket
// 缓冲填充满不满，也不量墙钟。
type blockingWriteConn struct {
	request []byte
	readOff int

	writeEntered chan struct{}
	writeGate    chan struct{}
	closeOnce    sync.Once
	closes       atomic.Int64

	remote net.Addr
}

func newBlockingWriteConn() *blockingWriteConn {
	return &blockingWriteConn{
		request:      []byte(connectRequest),
		writeEntered: make(chan struct{}),
		writeGate:    make(chan struct{}),
		remote:       &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 443},
	}
}

func (c *blockingWriteConn) Read(p []byte) (int, error) {
	if c.readOff < len(c.request) {
		n := copy(p, c.request[c.readOff:])
		c.readOff += n
		return n, nil
	}
	return 0, net.ErrClosed // 首请求之后立刻 EOF，模拟对端发完请求就不读也不再写
}

func (c *blockingWriteConn) Write(p []byte) (int, error) {
	close(c.writeEntered)
	<-c.writeGate
	return 0, net.ErrClosed // watcher 关连接后，Write 以错误返回
}

func (c *blockingWriteConn) Close() error {
	c.closeOnce.Do(func() {
		c.closes.Add(1)
		close(c.writeGate) // 放行阻塞中的 Write
	})
	return nil
}
func (c *blockingWriteConn) LocalAddr() net.Addr              { return c.remote }
func (c *blockingWriteConn) RemoteAddr() net.Addr             { return c.remote }
func (c *blockingWriteConn) SetDeadline(time.Time) error      { return nil }
func (c *blockingWriteConn) SetReadDeadline(time.Time) error  { return nil }
func (c *blockingWriteConn) SetWriteDeadline(time.Time) error { return nil }

// oneConnServer 只吐一条连接，之后 AcceptConn 阻塞到 ctx 取消，避免 acceptLoop 空转。
type oneConnServer struct {
	conn  tunnel.Conn
	given atomic.Bool
}

func (u *oneConnServer) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	if u.given.CompareAndSwap(false, true) {
		return u.conn, nil
	}
	<-time.After(50 * time.Millisecond)
	return nil, errors.New("no more connections")
}
func (u *oneConnServer) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, errors.New("not used")
}
func (u *oneConnServer) Close() error { return nil }

// TestConnectWriteWakesOnShutdown 钉住 CONNECT 分支的关停唤醒窗口：
// 首请求读完时 stopFirstRead() 让首读 watcher 退出，而 CONNECT 分支写 200 响应
// 的 conn.Write 既无写截止也无独立 watcher（GET/POST 分支有 closeRawConn +
// connDone 的完整 watcher）。对端读完请求后停止读取、TCP 零窗口时这次 Write
// 永久阻塞，Server.Close 的 wg.Wait 只能挂到 Proxy.Close 的 5s 兜底。
//
// 判据用可控连接把 Write 钉在 gate 上：观察到写已进入后 cancel s.ctx，
// 断言裸连接被 Close（从而放行 Write、handler 退出）。变异对照：去掉 CONNECT
// 分支的关停 watcher，gate 永不解除，断言当场失败，不依赖墙钟。
func TestConnectWriteWakesOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	raw := newBlockingWriteConn()
	s := &Server{
		underlay: &oneConnServer{conn: &pipeConn{Conn: raw}},
		ctx:      ctx,
		cancel:   cancel,
		connChan: make(chan tunnel.Conn, 4),
	}
	go s.acceptLoop()

	// 等 handler 走到写 200 响应那一步（Write 已进入并阻塞在 gate 上）。
	select {
	case <-raw.writeEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("handler 没有走到 CONNECT 的 200 响应写入，判据失效")
	}

	// 模拟 Server.Close 的取消：真正关停会先 cancel，再 wg.Wait。
	cancel()

	// watcher 必须在 ctx 取消后关掉裸连接，从而放行阻塞中的 Write。
	deadline := time.After(3 * time.Second)
	for raw.closes.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("ctx 取消后 CONNECT 的裸连接没被关闭：写 200 响应会永久阻塞，Close 白等")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
