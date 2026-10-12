package trojan

import (
	"context"
	"strings"
	"testing"

	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/statistic/memory"
)

// TestNewServerKeepsAuthenticatorRootCause 钉的是错误链的完整性：认证器创建失败时，
// 返回给上层的错误必须带上根因。以前 acquireAuth 是 log.Error 之后 return nil，
// NewServer 只能凭空造一句 "trojan failed to create authenticator"，于是
// `password: [""]` 这种配置最终报出去的话里没有"空密码"三个字
// （真机实测：最后两行只剩 trojan failed to create authenticator）。
func TestNewServerKeepsAuthenticatorRootCause(t *testing.T) {
	Auth = nil // 强制走"重建认证器"分支，别复用别的测试留下的实例

	cfg := &Config{}
	cfg.MySQL.Enabled = false
	ctx := config.WithConfig(context.Background(), Name, cfg)
	ctx = config.WithConfig(ctx, memory.Name, &memory.Config{Passwords: []string{""}})

	_, err := NewServer(ctx, nil)
	if err == nil {
		t.Fatal("空密码配置下 NewServer 竟然成功了")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("错误链把根因丢了，只剩一句通用文案：%v", err)
	}
}
