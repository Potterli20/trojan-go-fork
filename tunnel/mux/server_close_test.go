package mux

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtaci/smux"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// countedTunnelConn 记录自己有没有被关掉，不依赖真实 socket 的 FIN/RST 时序。
type countedTunnelConn struct {
	net.Conn
	closed atomic.Bool
}

func (c *countedTunnelConn) Close() error {
	c.closed.Store(true)
	return c.Conn.Close()
}

func (c *countedTunnelConn) Metadata() *tunnel.Metadata { return nil }

// fakeUnderlay 只做两件事：交出一条备好的连接，然后在 Close 后让 Accept 报错。
// 这正好复现真实下层 tunnel 的行为——trojan/tls 的 Close 只关自己的监听器和
// 还没被取走的连接，已经交给上层（mux）的连接不在其回收范围内。
type fakeUnderlay struct {
	incoming chan tunnel.Conn
	done     chan struct{}
	once     sync.Once
}

func (f *fakeUnderlay) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	select {
	case conn := <-f.incoming:
		return conn, nil
	case <-f.done:
		return nil, errors.New("fake underlay closed")
	}
}

func (f *fakeUnderlay) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, errors.New("fake underlay has no packet")
}

func (f *fakeUnderlay) Close() error {
	f.once.Do(func() { close(f.done) })
	return nil
}

var _ tunnel.Server = (*fakeUnderlay)(nil)

// TestServerCloseWakesEstablishedSessions 盯 mux 服务端的关停。
//
// handleSession 阻塞在 session.AcceptStream() 上，而这个调用既不看 s.ctx，
// 也不会因为下层监听器关闭而返回错误（会话的连接是上层交下来的）。于是
// Close = cancel → underlay.Close → wg.Wait 在「存在至少一个已建立会话」时
// 永久挂住，每个会话留下 1 个 goroutine + 1 个 smux.Session + 1 个 fd。
// 独立进程里外层的 waitBounded(5s) 只是把挂死变成超时日志，资源照样没回收。
//
// 修复前：Close 不返回，且那条连接从没被关掉。
func TestServerCloseWakesEstablishedSessions(t *testing.T) {
	clientSide, serverSideRaw := net.Pipe()
	defer clientSide.Close()

	readyConn := &countedTunnelConn{Conn: serverSideRaw}
	underlay := &fakeUnderlay{
		incoming: make(chan tunnel.Conn, 1),
		done:     make(chan struct{}),
	}
	underlay.incoming <- readyConn

	server, err := NewServer(context.Background(), underlay)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	session, err := smux.Client(clientSide, smux.DefaultConfig())
	if err != nil {
		t.Fatalf("smux.Client: %v", err)
	}
	defer session.Close()

	// 等会话真正建好并登记，否则 Close 可能跑在 handleConn 之前，判据就失真
	deadline := time.Now().Add(5 * time.Second)
	for {
		server.sessionLock.Lock()
		registered := len(server.sessions)
		server.sessionLock.Unlock()
		if registered == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("会话始终没有建立起来，判据无法成立")
		}
		time.Sleep(10 * time.Millisecond)
	}

	closed := make(chan error, 1)
	go func() { closed <- server.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Logf("Close 返回错误（非判据）: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Server.Close 被在途会话挂死：AcceptStream 不受 ctx 唤醒")
	}

	if !readyConn.closed.Load() {
		t.Fatal("关停后会话的底层连接没有被关闭（fd 泄漏）")
	}
	// 客户端侧也必须看到这条流断了，而不是继续对着一端已死的连接等
	_ = clientSide.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = clientSide.Read(make([]byte, 32))
	if err == nil {
		t.Fatal("服务端关停后会话仍可读取")
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatalf("等待服务端断开超时: %v", err)
	}
}
