package trojan

import (
	"context"
	"testing"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/statistic"
	"github.com/Potterli20/trojan-go-fork/statistic/memory"
	"github.com/Potterli20/trojan-go-fork/tunnel/freedom"
	"github.com/Potterli20/trojan-go-fork/tunnel/transport"
)

// TestNewClientReleasesAuthenticatorOnFailure 盯的是"认证成功后才失败"这条出口：
// NewClient 先 statistic.NewAuthenticator（登记进全局 createdAuth），之后才检查
// 口令列表；列表为空就返回 "no valid user found"，而那一刻的 cancel() 只能关掉
// 本函数派生的 ctx——认证器内部另派了 ctx，所以它不会被停掉。结果是每次配置错误
// 都留下一条永久登记项；配了 sqlite 时连 batchTrafficUpdater goroutine 和 DB 句柄
// 一起漏（memory 后端注释原话：生命周期由自身 Close 控制）。
func TestNewClientReleasesAuthenticatorOnFailure(t *testing.T) {
	base := statistic.CreatedAuthenticatorCount()
	port := common.PickPort("tcp", "127.0.0.1")

	ctx := context.Background()
	ctx = config.WithConfig(ctx, Name, &Config{
		RemoteHost: "127.0.0.1",
		RemotePort: port,
	})
	ctx = config.WithConfig(ctx, memory.Name, &memory.Config{Passwords: []string{}}) // 空口令 → 走失败出口
	ctx = config.WithConfig(ctx, freedom.Name, &freedom.Config{})                    // transport.NewClient 会走到 freedom
	ctx = config.WithConfig(ctx, transport.Name, &transport.Config{
		LocalHost:  "127.0.0.1",
		LocalPort:  port,
		RemoteHost: "127.0.0.1",
		RemotePort: port,
	})

	tcpClient, err := transport.NewClient(ctx, nil)
	common.Must(err)

	if _, err := NewClient(ctx, tcpClient); err == nil {
		t.Fatal("空口令列表本该失败，却成功了——门禁无法再复现该泄露")
	}

	if got := statistic.CreatedAuthenticatorCount(); got != base {
		t.Fatalf("建客户端失败后认证器没有被归还：登记数 %d，基线 %d"+
			"（失败路径必须 ReleaseAuthenticator，否则配置写错一次就漏一份）", got, base)
	}
}
