package tls

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/tunnel/transport"
)

// TestCloseWakesStalledHandshake 盯的是关停时序里那处"上限形同虚设"：
// Close 先前只等 handshakes 计数 500ms，然后紧跟一个**无界**的 s.wg.Wait()。
// 而 handler 本身就是 wg 成员，握手的读时限是 firstByteTimeout(30s)，
// 所以一条连一个字节都不发的 TCP 连接就能让 Close 实际等满 30s——
// 那个 500ms 完全被抵消；在真实进程里只是被 proxy 的 5s 兜底遮住，
// 表现为"停机慢一点"并附带一条 forced-release 告警。
//
// 判据：一条进入握手后就静默的连接，必须让 Close 在有界时间内返回。
// 等待条件用轮询在途登记表（同包可见），而不是 sleep 猜测，确保测的是
// "handler 确实卡在 Handshake 里"这一状态。
func TestCloseWakesStalledHandshake(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "server.crt")
	keyPath := filepath.Join(dir, "server.key")
	if err := os.WriteFile(certPath, []byte(rsa2048Cert), 0o600); err != nil {
		t.Fatalf("写入测试证书失败: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte(rsa2048Key), 0o600); err != nil {
		t.Fatalf("写入测试私钥失败: %v", err)
	}

	port := common.PickPort("tcp", "127.0.0.1")
	ctx := config.WithConfig(context.Background(), transport.Name, &transport.Config{
		LocalHost:  "127.0.0.1",
		LocalPort:  port,
		RemoteHost: "127.0.0.1",
		RemotePort: port,
	})
	ctx = config.WithConfig(ctx, Name, &Config{
		RemoteHost: "127.0.0.1",
		RemotePort: port,
		TLS: TLSConfig{
			CertPath:      certPath,
			KeyPath:       keyPath,
			CertCheckRate: 1,
		},
	})

	tcpServer, err := transport.NewServer(ctx, nil)
	common.Must(err)
	server, err := NewServer(ctx, tcpServer)
	common.Must(err)

	// 只建立 TCP 连接，不发送任何 TLS 字节：handler 会卡在 Handshake 里
	raw, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		t.Fatalf("连接测试服务失败: %v", err)
	}
	defer raw.Close() //gosec:disable -- 错误忽略：测试清理

	deadline := time.Now().Add(5 * time.Second)
	for {
		server.pendingMutex.Lock()
		pending := len(server.pendingConns)
		server.pendingMutex.Unlock()
		if pending > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("这条静默连接始终没有进入在途握手登记表：测试没能构造出\"卡在 Handshake\"的状态")
		}
		time.Sleep(10 * time.Millisecond)
	}

	closed := make(chan struct{})
	go func() {
		server.Close() //gosec:disable -- 错误忽略：测试清理
		close(closed)
	}()

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close 没有叫醒卡在 Handshake 的 handler：只能等满 firstByteTimeout(30s)，" +
			"握手阶段的有界等待被随后的无界 wg.Wait 完全抵消")
	}
}
