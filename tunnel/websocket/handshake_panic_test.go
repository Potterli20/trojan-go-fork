package websocket

import (
	"context"
	"net"
	"net/http"
	"testing"

	"github.com/coder/websocket"
)

func (s *Server) pendingCount() int {
	s.pendingMutex.Lock()
	defer s.pendingMutex.Unlock()
	return len(s.pendingConns)
}

// TestHandshakePanicReleasesPendingAndChildCtx 盯的是握手收尾的 panic 路径。
// AcceptConn 里"注销在途登记 / 解除握手时限 / 取消子 ctx"三段收尾如果是裸顺序调用，
// 只要 websocket.Accept 在解析对端可控的升级请求时 panic，三段全都不执行；而这条
// 调用链上面（trojan/server.go 与 tls/server.go 的 handler）都带 recover，进程照跑，
// 于是每命中一次就永久多留一个 pendingConns 表项，外加一个挂在 s.ctx 下的子 ctx
// ——cancel 没人调，父 ctx 不退出它就一直挂在 children 上，直到进程结束。
func TestHandshakePanicReleasesPendingAndChildCtx(t *testing.T) {
	peer, raw := net.Pipe()
	defer peer.Close() //gosec:disable -- 错误忽略：测试清理
	probe := &deadlineProbe{Conn: raw}
	srv := newHandshakeServer(t, &handoffServer{conn: probe})
	go peer.Write(upgradeRequest("/ws")) //gosec:disable -- 错误忽略：测试夹具

	var stashed context.Context
	orig := acceptHandshake
	t.Cleanup(func() { acceptHandshake = orig })
	acceptHandshake = func(ctx context.Context, _ http.ResponseWriter, _ *http.Request, _ *websocket.AcceptOptions) (*websocket.Conn, error) {
		stashed = ctx
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

	if stashed == nil {
		t.Fatal("用例没有走到握手这一步，判据失效")
	}
	if n := srv.pendingCount(); n != 0 {
		t.Fatalf("panic 之后在途握手登记表还剩 %d 条：Close 再也关不到它，"+
			"表项连同连接对象一起永久驻留", n)
	}
	if probe.deadlineStillArmed() {
		t.Fatal("panic 之后握手时限没有解除：这条连接被重定向时仍带着没做完的握手截止时间")
	}
	if err := stashed.Err(); err != context.Canceled {
		t.Fatalf("panic 之后握手子 ctx 没有被取消（err=%v）：它挂在 s.ctx 上，"+
			"每命中一次多一份，父 ctx 不退出就一直留着", stashed.Err())
	}
}

// TestHandshakeSuccessKeepsChildCtxAlive 是上一条的对照：正常移交上游的会话，
// 其 ctx 不能被收尾逻辑取消，否则会话刚建好就被自己的 ctx 掐死。
func TestHandshakeSuccessKeepsChildCtxAlive(t *testing.T) {
	peer, raw := net.Pipe()
	defer peer.Close() //gosec:disable -- 错误忽略：测试清理
	probe := &deadlineProbe{Conn: raw}
	srv := newHandshakeServer(t, &handoffServer{conn: probe})

	resp := make(chan int, 1)
	stopPeer := make(chan struct{})
	go func() {
		peer.Write(upgradeRequest("/ws")) //gosec:disable -- 错误忽略：测试夹具
		buf := make([]byte, 256)
		n, _ := peer.Read(buf)
		resp <- n
		<-stopPeer
		peer.Close() //gosec:disable -- 错误忽略：测试清理
	}()

	var stashed context.Context
	orig := acceptHandshake
	t.Cleanup(func() { acceptHandshake = orig })
	acceptHandshake = func(ctx context.Context, w http.ResponseWriter, req *http.Request, opts *websocket.AcceptOptions) (*websocket.Conn, error) {
		stashed = ctx
		return websocket.Accept(w, req, opts)
	}

	conn, err := srv.AcceptConn(&Tunnel{})
	if err != nil {
		t.Fatalf("正常对端的握手应当成功：%v", err)
	}
	<-resp
	if n := srv.pendingCount(); n != 0 {
		t.Fatalf("握手成功后在途登记表应清零，实得 %d", n)
	}
	if stashed.Err() != nil {
		t.Fatalf("移交上游的会话其 ctx 已被收尾逻辑取消（%v）", stashed.Err())
	}
	close(stopPeer)
	conn.Close() //gosec:disable -- 错误忽略：测试清理
	srv.Close()  //gosec:disable -- 错误忽略：测试清理
}
