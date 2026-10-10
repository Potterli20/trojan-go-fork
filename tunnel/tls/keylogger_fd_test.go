package tls

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Potterli20/trojan-go-fork/config"
)

func openFDCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("无法读取 /proc/self/fd，跳过 fd 计数： %v", err)
	}
	return len(entries)
}

// TestNewClientReleasesKeyLoggerOnFailure 盯的是：NewClient 先按配置打开 key log 文件
// （client.go:231），之后任何一条错误返回（此处是证书加载失败）都不持有 client 对象，
// 于是那个 io.WriteCloser 连同 fd 就再也关不掉了——Client.Close 是唯一的归还入口，
// 而失败路径根本没构造出 Client。反复建栈（嵌入式调用、测试、热重载）会稳定漏 fd。
//
// 判据用 fd 计数：跑 N 次失败，泄漏数应与 N 同量级；修复后必须回到基线。
func TestNewClientReleasesKeyLoggerOnFailure(t *testing.T) {
	dir := t.TempDir()
	keyLog := filepath.Join(dir, "keys.log")
	// loadCert 只在"读不到文件"时返回错误（内容解析不了只会 Warn 后继续），
	// 所以用一个不存在的路径才会走到那条错误返回上。
	missingCert := filepath.Join(dir, "does-not-exist.crt")

	const attempts = 20
	before := openFDCount(t)

	for range attempts {
		ctx := config.WithConfig(context.Background(), Name, &Config{
			RemoteHost: "127.0.0.1",
			RemotePort: 443,
			TLS: TLSConfig{
				KeyLogPath: keyLog,
				CertPath:   missingCert,
			},
		})
		if _, err := NewClient(ctx, nil); err == nil {
			t.Fatal("用一个坏证书本该失败，却成功了——门禁无法再复现该泄露")
		}
	}

	after := openFDCount(t)
	if leaked := after - before; leaked > attempts/2 {
		t.Fatalf("%d 次构建失败留下 %d 个未关闭的 fd：keyLogger 在错误路径上没人归还", attempts, leaked)
	}
}
