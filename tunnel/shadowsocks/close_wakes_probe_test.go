package shadowsocks

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type silentConn struct{ net.Conn }

func (c *silentConn) Metadata() *tunnel.Metadata { return nil }

type oneShotServer struct{ conn tunnel.Conn }

func (s *oneShotServer) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	c := s.conn
	s.conn = nil
	if c == nil {
		return nil, io.EOF
	}
	return c, nil
}

func (s *oneShotServer) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, io.EOF
}

func (s *oneShotServer) Close() error { return nil }

func (s *Server) pendingCount() int {
	s.pendingMutex.Lock()
	defer s.pendingMutex.Unlock()
	return len(s.pendingConns)
}

func waitUntil(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// 静默对端让 AEAD 探测读卡在 probeTimeout(10s) 里时，Close 必须当场叫醒它。
// 同步点是 pendingConns：修复后连接在读探测字节之前就已登记；旧实现没有登记表，
// 轮询超时即红（不是墙钟猜测）。
func TestCloseWakesProbeRead(t *testing.T) {
	peer, raw := net.Pipe()
	defer peer.Close() //gosec:disable -- 错误忽略：测试清理

	cfg := &Config{
		RemoteHost: "127.0.0.1",
		RemotePort: 80,
		Shadowsocks: ShadowsocksConfig{
			Enabled:  true,
			Method:   "AES-128-GCM",
			Password: "test",
		},
	}
	s, err := NewServer(config.WithConfig(context.Background(), Name, cfg), &oneShotServer{conn: &silentConn{Conn: raw}})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	accepted := make(chan error, 1)
	go func() {
		_, err := s.AcceptConn(nil)
		accepted <- err
	}()

	if !waitUntil(t, 2*time.Second, func() bool { return s.pendingCount() == 1 }) {
		t.Fatal("探测读阶段连接未登记在途：Close 叫不醒它")
	}

	start := time.Now()
	s.Close() //gosec:disable -- 错误忽略：测试清理
	select {
	case <-accepted:
		t.Logf("Close 在 %v 内叫醒了探测读", time.Since(start).Round(time.Millisecond))
	case <-time.After(2 * time.Second):
		t.Fatal("Close 后 AcceptConn 未在 2s 内返回：关停要白等满 10s 探测时限")
	}
}
