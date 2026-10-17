package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/tunnel/trojan"
)

// TestTrojanStackFailsClosedOnUnauthenticatedPublicAPI 钉住接线本身。
//
// 光有 checkAPIExposure 不够：`api.RunService` 的返回值在 tunnel/trojan 的两个调用点都被
// 丢进 goroutine 里，所以闸门必须由 NewServer/NewClient **同步**调用，才能让建栈失败、
// 进程以非零退出码结束。少了这一步，"失败关闭"就退化成静默：代理照常跑、API 悄悄不在、
// 日志一行都没有（docs/configuration.md 第 2 条记的就是这个形态）。
//
// 检查放在任何资源分配之前，所以这里可以用 nil underlay 与最小配置：闸门必须在
// acquireAuth、监听 bind、redirector 之前就把请求挡回来。
func TestTrojanStackFailsClosedOnUnauthenticatedPublicAPI(t *testing.T) {
	badAPI := &Config{API: APIConfig{Enabled: true, APIHost: "203.0.113.7", APIPort: 10000}}

	ctx := config.WithConfig(context.Background(), Name, badAPI)
	ctx = config.WithConfig(ctx, trojan.Name, &trojan.Config{})

	if _, err := trojan.NewServer(ctx, nil); err == nil {
		t.Fatal("服务端栈：无凭据的公网 API 监听被放行了，NewServer 没有同步调用暴露面检查")
	} else if !strings.Contains(err.Error(), "credential") {
		t.Fatalf("服务端栈报错了，但不是暴露面闸门（看不出该改哪个配置）: %v", err)
	}

	if _, err := trojan.NewClient(ctx, nil); err == nil {
		t.Fatal("客户端栈：无凭据的公网 API 监听被放行了，NewClient 没有同步调用暴露面检查")
	} else if !strings.Contains(err.Error(), "credential") {
		t.Fatalf("客户端栈报错了，但不是暴露面闸门: %v", err)
	}
	// 正向对照不放在这里：上面两处都要求错误文案里必须出现 credential，所以"NewServer
	// 因为别的原因也报错"这种假阳性已经被排除了。token 配齐之后能不能真起服务，
	// 由 TestStreamRPCWithTokenReceivesRecords 在 bufconn 上端到端验证。
}
