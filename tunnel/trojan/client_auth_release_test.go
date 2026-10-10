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

// TestClientReleasesAuthenticator 盯的是客户端侧漏掉的一半释放：
// NewClient 用 statistic.NewAuthenticator(ctx, memory.Name) 取认证器，该函数按 ctx
// 为键登记进全局 createdAuth（只增不删）。服务端早已成对调用 ReleaseAuthenticator，
// 客户端没有——于是每建一个 client 实例就永久留住一条表项、整棵 User 对象图（含
// ipTable）以及 sqlite 的 DB 句柄（Authenticator.Close 是唯一的 pst.Close 入口）。
// 反复建/关（多实例、嵌入式调用、测试）就会单调增长。
func TestClientReleasesAuthenticator(t *testing.T) {
	base := statistic.CreatedAuthenticatorCount()
	serverPort := common.PickPort("tcp", "127.0.0.1")

	for range 3 {
		ctx := context.Background()
		ctx = config.WithConfig(ctx, Name, &Config{
			RemoteHost: "127.0.0.1",
			RemotePort: serverPort,
		})
		ctx = config.WithConfig(ctx, memory.Name, &memory.Config{Passwords: []string{"password"}})
		ctx = config.WithConfig(ctx, transport.Name, &transport.Config{
			LocalHost:  "127.0.0.1",
			LocalPort:  serverPort,
			RemoteHost: "127.0.0.1",
			RemotePort: serverPort,
		})
		ctx = config.WithConfig(ctx, freedom.Name, &freedom.Config{})

		tcpClient, err := transport.NewClient(ctx, nil)
		common.Must(err)

		client, err := NewClient(ctx, tcpClient)
		common.Must(err)

		if got := statistic.CreatedAuthenticatorCount(); got != base+1 {
			t.Fatalf("建一个客户端应有 1 条认证器登记：基线 %d，实际 %d", base, got)
		}

		common.Must(client.Close())

		if got := statistic.CreatedAuthenticatorCount(); got != base {
			t.Fatalf("客户端 Close 后认证器表没有回收：%d 条残留（应为基线 %d）"+
				"——Close 必须调用 statistic.ReleaseAuthenticator(c.ctx)", got-base, base)
		}
	}
}
