package simplesocks

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// panickingUnderlay 在前 panicsLeft 次 AcceptConn 里直接 panic，模拟下层（websocket 的
// 升级请求解析、shadowsocks 的密文探测这一类）在**未认证对端可控字节**上崩掉。
// 其余行为沿用 header_timeout_test.go 里的 fakeUnderlay。
type panickingUnderlay struct {
	*fakeUnderlay
	panicsLeft atomic.Int64
	calls      atomic.Int64
}

func newPanickingUnderlay(ctx context.Context, buffered int, panics int64) *panickingUnderlay {
	p := &panickingUnderlay{fakeUnderlay: newFakeUnderlay(ctx, buffered)}
	p.panicsLeft.Store(panics)
	return p
}

func (p *panickingUnderlay) AcceptConn(t tunnel.Tunnel) (tunnel.Conn, error) {
	p.calls.Add(1)
	if p.panicsLeft.Add(-1) >= 0 {
		panic("corrupt bytes from an unauthenticated peer")
	}
	return p.fakeUnderlay.AcceptConn(t)
}

// panicReadConn 的 Read 在解析地址字段时 panic：simplesocks 的 acceptLoop 是**在循环体里**
// 串行调用 readHeader 的，所以这道边界必须覆盖到它，而不只是 underlay 那一次调用。
type panicReadConn struct{ *stalledConn }

func newPanicReadConn() *panicReadConn {
	return &panicReadConn{stalledConn: newStalledConn()}
}

func (c *panicReadConn) Read([]byte) (int, error) {
	panic("invalid address length byte from peer")
}

// TestAcceptLoopSurvivesUnderlayPanic 钉住 accept 循环里的 panic 边界。
// 修复前：`s.underlay.AcceptConn(&Tunnel{})` 写在循环体里，全仓只有 mux/tls/trojan 三处
// recover、且都在 per-conn handler 的 goroutine 里，main 也没有顶层 recover——下层在解析
// 对端字节时 panic 会一路冒出 accept 协程，命中一次整个进程就没了。
//
// 判据：连撞两次 panic 之后，第三条合法会话仍然要被读到并放行。循环死了这条就超时。
func TestAcceptLoopSurvivesUnderlayPanic(t *testing.T) {
	ctx := t.Context()
	underlay := newPanickingUnderlay(ctx, 2, 2)

	goodAddr := &tunnel.Address{AddressType: tunnel.DomainName, DomainName: "after-panic.example.com", Port: 443}
	underlay.conns <- newHeaderConn(&tunnel.Metadata{Command: Connect, Address: goodAddr})

	server, err := NewServer(ctx, underlay)
	common.Must(err)
	t.Cleanup(func() { server.Close() }) //gosec:disable -- 错误忽略：测试清理

	md, err := waitMetadata(server, 3*time.Second)
	if err != nil {
		t.Fatalf("underlay 连撞两次 panic 之后没有再放行会话：accept 循环已经被带走 = %v", err)
	}
	if md.Address == nil || md.Address.DomainName != "after-panic.example.com" {
		t.Fatalf("放行的会话地址不对：%v", md.Address)
	}
	// 边界只吃掉 panic，不该把 underlay 的调用算少：至少 2 次 panic + 1 次交付
	// （循环会立刻再要下一条，所以只判下界）
	if got := underlay.calls.Load(); got < 3 {
		t.Fatalf("underlay 只被调用 %d 次，期望至少 3 次（2 次 panic + 1 次交付）", got)
	}
}

// TestHeaderPanicClosesConnAndLoopKeepsServing 钉住边界另外两个必做项：
// ① panic 的那条连接必须被关闭——否则循环虽然活着，每命中一次却留一个 fd
// （这就是"panic 后进程照跑"变成"panic 后慢慢耗尽 fd"）；
// ② 读头在循环体里串行跑，它也要在边界内，不能只保护 underlay 那一次调用。
func TestHeaderPanicClosesConnAndLoopKeepsServing(t *testing.T) {
	ctx := t.Context()
	underlay := newPanickingUnderlay(ctx, 2, 0)

	bad := newPanicReadConn()
	goodAddr := &tunnel.Address{AddressType: tunnel.DomainName, DomainName: "after-header-panic.example.com", Port: 8443}
	underlay.conns <- bad
	underlay.conns <- newHeaderConn(&tunnel.Metadata{Command: Connect, Address: goodAddr})

	server, err := NewServer(ctx, underlay)
	common.Must(err)
	t.Cleanup(func() { server.Close() }) //gosec:disable -- 错误忽略：测试清理

	md, err := waitMetadata(server, 3*time.Second)
	if err != nil {
		t.Fatalf("读头 panic 之后没有再放行会话：accept 循环被这条对端字节带走了 = %v", err)
	}
	if md.Address == nil || md.Address.DomainName != "after-header-panic.example.com" {
		t.Fatalf("放行的会话地址不对：%v", md.Address)
	}
	if !bad.closed.Load() {
		t.Fatal("panic 的那条连接没有被回收：循环活着但每命中一次漏一个 fd")
	}
}

func waitMetadata(server *Server, within time.Duration) (*tunnel.Metadata, error) {
	got := make(chan *tunnel.Metadata, 1)
	go func() {
		conn, err := server.AcceptConn(&Tunnel{})
		if err != nil {
			got <- nil
			return
		}
		got <- conn.Metadata()
	}()
	select {
	case md := <-got:
		if md == nil {
			return nil, context.Canceled
		}
		return md, nil
	case <-time.After(within):
		return nil, context.DeadlineExceeded
	}
}
