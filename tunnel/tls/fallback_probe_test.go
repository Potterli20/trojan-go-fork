package tls

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/config"
)

// TestFallbackProbeHonorsContext 是 trojan 侧同型判据的姊妹条：TLS fallback 地址的
// 启动期探测过去也是裸 `net.Dial("tcp", ...)`——没有超时、不看 ctx。fallback 地址丢
// SYN 时 NewServer 里的这次同步探测挂满约 2 分钟才报错，期间建栈的主协程被钉住，
// Ctrl-C 与上层 cancel 全都无效。
//
// 探测是 NewServer 的第一件事，所以这里不需要真证书也不需要 underlay：判据只要
// "ctx 已取消 → 这次探测必须失败并报在 fallback 上"。旧写法会连上活着的诱饵、
// 把探测判成通过，然后才在证书那一步用另一条错误失败。
func TestFallbackProbeHonorsContext(t *testing.T) {
	decoy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen decoy: %v", err)
	}
	defer decoy.Close() //gosec:disable -- 错误忽略：测试清理
	host, portStr, err := net.SplitHostPort(decoy.Addr().String())
	if err != nil {
		t.Fatalf("split %v: %v", decoy.Addr(), err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port %q: %v", portStr, err)
	}

	ctx := config.WithConfig(context.Background(), Name, &Config{
		TLS: TLSConfig{
			FallbackHost: host,
			FallbackPort: port,
		},
	})
	cancelled, cancel := context.WithCancel(ctx)
	cancel() // 探测必须被这次取消打断，而不是照样连上去

	start := time.Now()
	server, err := NewServer(cancelled, nil)
	elapsed := time.Since(start)
	if err == nil {
		server.Close() //gosec:disable -- 错误忽略：测试清理
		t.Fatal("ctx 已取消而 fallback 探测照样成功：裸 net.Dial 不看 ctx，" +
			"换成黑洞 fallback 时这里会挂满约 2 分钟，期间 Ctrl-C 无效")
	}
	if !strings.Contains(err.Error(), "invalid fallback address") {
		t.Fatalf("失败原因不是 fallback 探测（%v）：判据没走到要钉的那一步", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("取消之后过了 %v 才返回：探测拨号没有接 ctx", elapsed.Round(time.Millisecond))
	}
}
