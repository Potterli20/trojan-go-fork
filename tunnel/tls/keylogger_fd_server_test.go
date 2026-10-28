package tls

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Potterli20/trojan-go-fork/config"
)

// TestNewServerReleasesKeyLoggerOnFailure 是 keylogger_fd_test.go 里客户端那条判据的姊妹。
//
// NewServer 同样在构造出 server 对象**之前**就按配置打开了 key log 文件，而 Server.Close()
// 是这个句柄唯一的归还入口。此后唯一会失败的一步是曲线名校验：拼错一个名字（或少写一位）
// 加上配了 key_log，就得到一条"报错退出、但句柄没人认领"的路径。对单次启动只是浪费一个
// fd，对库化调用方（反复建栈、热重载）是稳定增长的文件描述符数。
//
// 判据用 fd 计数：N 次失败之后必须回到基线，而不是留下与 N 同量级的未关闭 fd。
func TestNewServerReleasesKeyLoggerOnFailure(t *testing.T) {
	dir := t.TempDir()
	keyLog := filepath.Join(dir, "keys.log")

	const attempts = 20
	before := openFDCount(t)

	for range attempts {
		ctx := config.WithConfig(context.Background(), Name, &Config{
			RemoteHost: "127.0.0.1",
			RemotePort: 443,
			TLS: TLSConfig{
				KeyLogPath: keyLog,
				KeyPath:    "server-rsa2048.key",
				CertPath:   "server-rsa2048.crt",
				// 少写一位的 X25519MLKEM768：必须被 ValidateCurvePreferences 拒掉，
				// 而不是无声落回默认集合（这是 2026-10-27 那条改名表校验的既有语义）。
				CurvePreferences: []string{"X25519MLKEM76"},
			},
		})
		if _, err := NewServer(ctx, nil); err == nil {
			t.Fatal("用一个坏曲线名本该失败，却成功了——门禁无法再复现该泄露")
		}
	}

	after := openFDCount(t)
	if leaked := after - before; leaked > attempts/2 {
		t.Fatalf("%d 次构建失败留下 %d 个未关闭的 fd：keyLogger 在错误路径上没人归还", attempts, leaked)
	}
}
