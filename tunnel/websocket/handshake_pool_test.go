package websocket

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
)

// 握手 bufio 池的账目判据：借出计数必须回到起点。成功会话把归还责任交给
// InboundConn（存活期跟随整个会话），失败握手在 AcceptConn 返回前归还。
// 为负即双归还——比漏还严重，两个会话会共享同一块底层数组互相串数据。
func TestHandshakeBufferAccounting(t *testing.T) {
	base := borrowedHandshakeBufs.Load()

	// 成功握手：3 条，关两次 Close 验证归还只发生一次。
	for i := 0; i < 3; i++ {
		srv, peer, _ := startHandshake(t, "/ws")
		conn, err := srv.AcceptConn(&Tunnel{})
		if err != nil {
			t.Fatalf("第 %d 条成功握手失败: %v", i, err)
		}
		// coder 的 Close 会发 close 帧并等回执：先让对端撤，避免白等库内写超时
		_ = peer.Close() //gosec:disable -- 错误忽略：测试清理
		_ = conn.Close() //gosec:disable -- 错误忽略：测试清理
		_ = conn.Close() //gosec:disable -- 错误忽略：测试清理：重复 Close 不得重复归还
		_ = srv.Close()
	}

	// 失败握手（路径不匹配 → 走 redirector）：AcceptConn 返回时缓冲必须已归还。
	for i := 0; i < 3; i++ {
		srv, peer, _ := startHandshake(t, "/not-ws")
		if _, err := srv.AcceptConn(&Tunnel{}); err == nil {
			t.Fatalf("第 %d 条错误路径握手竟然成功", i)
		}
		_ = peer.Close() //gosec:disable -- 错误忽略：测试清理
		_ = srv.Close()
	}

	if got := borrowedHandshakeBufs.Load(); got != base {
		t.Fatalf("池账目没有回到起点: base=%d got=%d（负值即双归还，正值即漏还）", base, got)
	}
}

func startHandshake(t *testing.T, path string) (*Server, net.Conn, *deadlineProbe) {
	t.Helper()
	peer, raw := net.Pipe()
	t.Cleanup(func() { peer.Close() }) //gosec:disable -- 错误忽略：测试清理
	probe := &deadlineProbe{Conn: raw}
	srv := newHandshakeServer(t, &handoffServer{conn: probe})
	go func() {
		_, _ = peer.Write(upgradeRequest(path)) //gosec:disable -- 错误忽略：测试夹具
		buf := make([]byte, 256)
		_, _ = peer.Read(buf) // 读走 101；之后保持静默，关连接由测试发起
	}()
	return srv, peer, probe
}

// 每次握手省掉的正是这两块 4KB 的分配（新实现 0 allocs/op）。
func BenchmarkHandshakeBufferPool(b *testing.B) {
	b.Run("pooled", func(b *testing.B) {
		src := strings.NewReader("")
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			r := getHandshakeReader(src)
			w := getHandshakeWriter(io.Discard)
			putHandshakeReader(r)
			putHandshakeWriter(w)
		}
	})
	b.Run("fresh", func(b *testing.B) {
		src := strings.NewReader("")
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_ = bufio.NewReaderSize(src, handshakeBufSize)
			_ = bufio.NewWriterSize(io.Discard, handshakeBufSize)
		}
	})
}