package websocket

import (
	"testing"
	"time"
)

// 静默对端（只连接不发字节）阻塞在 ReadRequest 时，Close 必须当场叫醒它，
// 而不是等满 handshakeTimeout(30s)。判据的同步点是 pendingConns：修复后
// 连接在读阶段就已登记，轮询到表项出现再 Close；旧实现读阶段永不登记，
// 轮询超时即红（不是墙钟猜测）。
func TestCloseWakesUpgradeRequestRead(t *testing.T) {
	srv, _, peer := newPanicServer(t) // peer 永不写：AcceptConn 会一直堵在读升级请求上

	accepted := make(chan error, 1)
	go func() {
		_, err := srv.AcceptConn(&Tunnel{})
		accepted <- err
	}()

	if !waitUntil(2*time.Second, func() bool { return srv.pendingCount() == 1 }) {
		t.Fatal("读阶段连接未登记在途：Close 叫不醒它")
	}

	start := time.Now()
	srv.Close() //gosec:disable -- 错误忽略：测试清理
	select {
	case <-accepted:
		t.Logf("Close 在 %v 内叫醒了读升级请求的 AcceptConn", time.Since(start).Round(time.Millisecond))
	case <-time.After(2 * time.Second):
		t.Fatal("Close 后 AcceptConn 未在 2s 内返回：关停要白等满 30s 握手时限")
	}

	_ = peer.Close() //gosec:disable -- 错误忽略：测试清理
}