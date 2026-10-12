//go:build linux

package tproxy

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// fakeListener 把预先准备好的连接交出去，用来构造「Accept 成功、但后续处理失败」的现场。
type fakeListener struct {
	conn net.Conn
	addr net.Addr
}

func (l *fakeListener) Accept() (net.Conn, error) {
	if l.conn == nil {
		<-time.After(time.Second)
		return nil, errors.New("fakeListener: 没有更多连接")
	}
	c := l.conn
	l.conn = nil
	return c, nil
}

func (l *fakeListener) Close() error { return nil }

func (l *fakeListener) Addr() net.Addr { return l.addr }

var _ net.Listener = (*fakeListener)(nil)

// countedConn 记录自己有没有被关掉。用 net.Pipe 的一半观察关闭，
// 不依赖真实 socket 的 FIN/RST 时序。
type countedConn struct {
	net.Conn
	closed atomic.Bool
}

func (c *countedConn) Close() error {
	c.closed.Store(true)
	return c.Conn.Close()
}

func newTestServer(l net.Listener) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	// AcceptConn 只用 tcpListener 与 ctx，所以不凑齐 NewServer 的其余字段——
	// 而且 NewServer 要走 IP_TRANSPARENT，没有 CAP_NET_ADMIN 根本起不来。
	return &Server{tcpListener: l, ctx: ctx, cancel: cancel}
}

// TestAcceptConnHandlesNonTCPSocket 盯两件事：
//  1. 不能 panic。原写法是裸类型断言 conn.(*net.TCPConn)，监听器交出的若不是
//     *net.TCPConn，断言就在 relay 的 accept 循环里炸掉整个进程；
//  2. 失败必须把 Accept 出来的 socket 关掉。relay 拿到错误只记一条日志就继续 Accept，
//     被丢下的那条连接没人回收，每来一条泄露一个 fd。
func TestAcceptConnHandlesNonTCPSocket(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	fake := &countedConn{Conn: serverSide}
	s := newTestServer(&fakeListener{conn: fake, addr: fake.LocalAddr()})
	defer s.cancel()

	done := make(chan struct{})
	var panicValue any
	go func() {
		// close 必须是 defer：panic 展开时会跳过后续语句，写成"调用完再 close"的话
		// 被测代码一 panic 我就只能等到超时，报出来的原因还是错的。
		defer close(done)
		defer func() { panicValue = recover() }()
		_, _ = s.AcceptConn(&Tunnel{})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("AcceptConn 5s 没有返回")
	}
	if panicValue != nil {
		t.Fatalf("AcceptConn 对非 *net.TCPConn 的连接 panic 了（裸类型断言），会把 relay 连进程一起打崩：%v", panicValue)
	}
	if !fake.closed.Load() {
		t.Fatal("AcceptConn 失败后没有关掉已经 Accept 出来的 socket：每条这样的连接泄露一个 fd")
	}
	_ = clientSide.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := clientSide.Read(make([]byte, 1)); err == nil {
		t.Fatal("服务端说关了连接，但对端仍读得到数据")
	}
	clientSide.Close() //gosec:disable -- 错误忽略：测试清理
}

// TestAcceptConnFallsBackToLocalAddr 钉住本包注释承诺的行为：TPROXY 模式不经过
// conntrack 改写，SO_ORIGINAL_DST 查询会失败，此时 LocalAddr 就是原始目的地址，
// 必须回退成功而不是报错。
//
// 这也是 tproxy 入站第一次有「真实 TCP 连接进来」的覆盖：nat run-type 的 source
// 就是它，而 nat 模式没有端到端测试的原因是 NewServer 需要 CAP_NET_ADMIN
// （IP_TRANSPARENT），不是没人管。
func TestAcceptConnFallsBackToLocalAddr(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	defer ln.Close() //gosec:disable -- 错误忽略：测试清理

	s := newTestServer(ln)
	defer s.cancel()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("拨号失败: %v", err)
	}
	defer client.Close() //gosec:disable -- 错误忽略：测试清理

	inbound, err := s.AcceptConn(&Tunnel{})
	if err != nil {
		t.Fatalf("没有 netfilter 标记时应当回退用 LocalAddr，实际报错：%v", err)
	}
	meta := inbound.Metadata()
	if meta == nil || meta.Address == nil {
		t.Fatal("回退路径没有给出目的地")
	}
	want := ln.Addr().(*net.TCPAddr)
	if !meta.Address.IP.Equal(want.IP) || meta.Address.Port != want.Port {
		t.Fatalf("回退的目的地是 %s，期望监听地址 %s", meta.Address.String(), want.String())
	}
	inbound.Close() //gosec:disable -- 错误忽略：测试清理
}
