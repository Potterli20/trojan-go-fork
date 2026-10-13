package redirector

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
)

// countedConn 观察「这条连接有没有被关掉」，不依赖真实 socket 的 FIN/RST 时序。
type countedConn struct {
	net.Conn
	closed atomic.Bool
}

func (c *countedConn) Close() error {
	c.closed.Store(true)
	return c.Conn.Close()
}

func newPair(t *testing.T) *countedConn {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
	})
	return &countedConn{Conn: server}
}

func newBareRedirector(capacity int) (*Redirector, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	return &Redirector{
		ctx:             ctx,
		cancel:          cancel,
		redirectionChan: make(chan *Redirection, capacity),
	}, cancel
}

// TestRedirectAfterCancelReclaimsConn 盯 Redirect 的丢弃分支。
//
// 调用方（trojan / tls / websocket / shadowsocks 的 fallback 分支）在 Redirect
// 返回后立刻 return，连接所有权已交出，唯一会关掉它的 defer 在 worker 的会话
// goroutine 里。旧实现在 ctx.Done 分支只打一行日志就把请求丢下，于是关停/reload
// 竞态中正阻塞在 send 上的未认证连接永远没人关闭。
//
// 判据：上下文已取消时，Redirect 必须把这条连接关掉。
func TestRedirectAfterCancelReclaimsConn(t *testing.T) {
	r, cancel := newBareRedirector(0) // 无缓冲且无消费者：send 分支永不成立，判据确定
	conn := newPair(t)
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Redirect(&Redirection{
			RedirectTo:  &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 80},
			InboundConn: conn,
		})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Redirect 在 ctx 取消后仍阻塞在 send 上")
	}
	if !conn.closed.Load() {
		t.Fatal("被丢弃的重定向请求没有关掉它的连接（fd 泄漏）")
	}
}

// TestCloseDrainsQueuedRedirections 盯 worker 退出后留在缓冲区里的请求。
// worker 走 ctx.Done 分支直接 return，不排空容量 64 的通道；旧实现 Close 之后
// 这些请求携带的连接再没人管。
func TestCloseDrainsQueuedRedirections(t *testing.T) {
	r, _ := newBareRedirector(64)
	conns := make([]*countedConn, 5)
	for i := range conns {
		conns[i] = newPair(t)
		r.Redirect(&Redirection{ // ctx 仍活着且有空位：请求进缓冲区
			RedirectTo:  &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 80},
			InboundConn: conns[i],
		})
	}
	if len(r.redirectionChan) != 5 {
		t.Fatalf("判据前提不成立：缓冲区里有 %d 条请求", len(r.redirectionChan))
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.Close()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close 挂住")
	}
	for i, conn := range conns {
		if !conn.closed.Load() {
			t.Fatalf("第 %d 条留在缓冲区里的请求没有回收连接", i)
		}
	}
}

// TestRedirectThroughClosedRedirector 用真实入口（NewRedirector + Close）走一遍：
// worker 已退出后递交的请求必须被就地回收，而不是随机落进缓冲区。
func TestRedirectThroughClosedRedirector(t *testing.T) {
	r := NewRedirector(context.Background())
	common.Must(r.Close())

	for i := range 16 { // 缓冲区有 64 个空位，旧实现在这里两个分支随机命中
		conn := newPair(t)
		r.Redirect(&Redirection{
			RedirectTo:  &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 80},
			InboundConn: conn,
		})
		if !conn.closed.Load() {
			t.Fatalf("第 %d 次：关停后递交的请求没有被回收（%d 条滞留在缓冲区）", i, len(r.redirectionChan))
		}
	}
}
