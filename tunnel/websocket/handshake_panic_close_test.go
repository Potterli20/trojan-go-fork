package websocket

import (
	"bufio"
	"net"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// closingProbe 在 deadlineProbe 之上再包一层，只多记一件事：这条连接到底有没有被关掉。
type closingProbe struct {
	tunnel.Conn
	closed atomic.Bool
}

func (p *closingProbe) Close() error {
	p.closed.Store(true)
	return p.Conn.Close() //gosec:disable -- 测试夹具：错误由调用方断言
}

// newPanicServer 造一个握手夹具服务端：underlay 只交出一条 net.Pipe 半边，
// 对端由测试自己掌握。
func newPanicServer(t *testing.T) (*Server, *closingProbe, net.Conn) {
	t.Helper()
	peer, raw := net.Pipe()
	t.Cleanup(func() { peer.Close() }) //gosec:disable -- 错误忽略：测试清理
	probe := &closingProbe{Conn: &deadlineProbe{Conn: raw}}
	srv := newHandshakeServer(t, &handoffServer{conn: probe})
	return srv, probe, peer
}

// TestPanicBeforePendingRegistrationClosesConn 钉住握手 panic 的**资源**收尾。
//
// AcceptConn 解析的是未认证对端可控的升级请求。父层的 tunnel.AcceptConnGuarded 只把
// panic 变成 error —— 它手里没有这条连接的句柄；而 panic 发生在 trackPending **之前**时
// 连接也还没进在途登记表，Server.Close 的排空同样够不着它。旧的收尾只有 handedOff 那一条
// defer（取消握手子 ctx），于是每命中一次就丢下一条没人引用的连接：fd 只能等运行时对
// netFD 的 finalizer 回收，TLS 会话状态、bufio 读写缓冲和这条连接的元数据在此之前一直
// 驻留堆上（AGENTS.md 原先写的"websocket 的 handedOff 负责资源收尾"与代码不符）。
// 注入点用 readUpgradeRequest，形态与 acceptHandshake 一致。
func TestPanicBeforePendingRegistrationClosesConn(t *testing.T) {
	srv, probe, peer := newPanicServer(t)
	go peer.Write(upgradeRequest("/ws")) //gosec:disable -- 错误忽略：测试夹具

	orig := readUpgradeRequest
	t.Cleanup(func() { readUpgradeRequest = orig })
	readUpgradeRequest = func(*bufio.Reader) (*http.Request, error) {
		panic("injected: panic while parsing a peer-controlled upgrade request")
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Error("注入的 panic 没有穿出 AcceptConn，判据失效")
			}
		}()
		_, _ = srv.AcceptConn(&Tunnel{})
	}()

	if !probe.closed.Load() {
		t.Fatal("panic 之后这条连接既没被关掉、也没登记在途：fd 与 TLS 会话状态只能等 GC 的 " +
			"netFD finalizer 回收，命中多少次就攒多少条")
	}
	if n := srv.pendingCount(); n != 0 {
		t.Fatalf("panic 之后在途登记表还剩 %d 条：Close 再也关不到它", n)
	}
}

// TestRedirectedConnIsNotClosedByHandshakeCleanup 是上一条的对照：认证失败/握手非法的
// 连接所有权归 redirector，它要继续在这条连接上拷贝给诱饵站。握手层若把它一起关掉，
// 对探测者就是"连上即断"的可分辨指纹，而不是一次正常的网站应答。
func TestRedirectedConnIsNotClosedByHandshakeCleanup(t *testing.T) {
	srv, probe, peer := newPanicServer(t)
	go peer.Write(upgradeRequest("/not-the-ws-path")) //gosec:disable -- 错误忽略：测试夹具

	conn, err := srv.AcceptConn(&Tunnel{})
	if err == nil {
		t.Fatal("路径不匹配的请求应当走重定向并返回错误，判据失效")
	}
	if conn != nil {
		t.Fatal("重定向路径不该交出连接对象")
	}
	if probe.closed.Load() {
		t.Fatal("连接被握手层的收尾关掉了：它正被 redirector 用来中继，关掉等于对探测者自曝")
	}
	if n := srv.pendingCount(); n != 0 {
		t.Fatalf("失败握手的在途计数应为 0，实得 %d", n)
	}
}

// TestClosingServerStillClosesRejectedConn 覆盖 trackPending 失败（服务已进入关停）这一条：
// 这里没有重定向，连接必须被关掉，否则关停时这条 fd 谁也不认。
func TestClosingServerStillClosesRejectedConn(t *testing.T) {
	srv, probe, peer := newPanicServer(t)
	go peer.Write(upgradeRequest("/ws")) //gosec:disable -- 错误忽略：测试夹具

	// 让 trackPending 失败：Close 之后 s.closing 置位，登记表不再接受新握手
	srv.Close() //gosec:disable -- 错误忽略：测试清理

	_, err := srv.AcceptConn(&Tunnel{})
	if err == nil {
		t.Fatal("关停中的服务端不该再接受一次握手：判据失效")
	}
	if !probe.closed.Load() {
		t.Fatal("服务关停中拒绝的握手没有关掉连接：这条 fd 从此没人持有")
	}
}
