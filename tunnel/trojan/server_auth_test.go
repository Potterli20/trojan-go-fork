package trojan

import (
	"context"
	"testing"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/statistic"
	"github.com/Potterli20/trojan-go-fork/statistic/memory"
	"github.com/Potterli20/trojan-go-fork/tunnel/transport"
)

// newAuthTestServer 造一个只用于认证器生命周期测试的服务端：
// 不启动转发，DisableHTTPCheck 让它不需要 misdirection 目标在线
func newAuthTestServer(t *testing.T, ctx context.Context) *Server {
	t.Helper()
	localPort := common.PickPort("tcp", "127.0.0.1")
	ctx = config.WithConfig(ctx, transport.Name, &transport.Config{
		LocalHost:  "127.0.0.1",
		LocalPort:  localPort,
		RemoteHost: "127.0.0.1",
		RemotePort: localPort,
	})
	underlay, err := transport.NewServer(ctx, nil)
	common.Must(err)
	t.Cleanup(func() { underlay.Close() })

	server, err := NewServer(config.WithConfig(ctx, Name, &Config{
		LocalHost:        "127.0.0.1",
		LocalPort:        common.PickPort("tcp", "127.0.0.1"),
		RemoteHost:       "127.0.0.1",
		RemotePort:       80,
		DisableHTTPCheck: true,
	}), underlay)
	common.Must(err)
	return server
}

// 多个 Server 实例（服务端栈里 TLS 分支和 websocket 分支各建一个 trojan Server）
// 共用一个认证器：只有最后一个实例关闭时才允许真正关闭认证器并从注册表移除
func TestSharedAuthenticatorRefcount(t *testing.T) {
	Auth = nil // 从干净状态开始
	ctx := config.WithConfig(t.Context(), memory.Name, &memory.Config{Passwords: []string{"password"}})

	first := newAuthTestServer(t, ctx)
	if Auth == nil {
		t.Fatal("authenticator not created on first server")
	}
	shared := Auth

	second := newAuthTestServer(t, ctx)
	if Auth != shared {
		t.Fatal("second server created a separate authenticator")
	}

	first.Close()
	if Auth != shared {
		t.Fatal("authenticator released while another server is still using it")
	}
	if cached, err := statistic.NewAuthenticator(second.authRef.ctx, memory.Name); err != nil || cached != shared {
		t.Fatalf("authenticator should still be registered and reusable, got %v (err %v)", cached, err)
	}

	// 重复 Close 不能多归还一次引用
	first.Close()
	second.Close()
	if Auth != nil {
		t.Fatal("authenticator still set after the last server closed")
	}

	// 注册表里的键必须已移除：同一个 ctx 再取会拿到新建实例而不是旧的那个
	fresh, err := statistic.NewAuthenticator(second.authRef.ctx, memory.Name)
	if err != nil {
		t.Fatalf("re-create after release: %v", err)
	}
	if fresh == shared {
		t.Fatal("createdAuth still holds the closed authenticator")
	}
	common.Must(statistic.ReleaseAuthenticator(second.authRef.ctx))

	third := newAuthTestServer(t, ctx)
	if Auth == shared {
		t.Fatal("stale authenticator reused after release")
	}
	third.Close()
	if Auth != nil {
		t.Fatal("authenticator not released by the last server")
	}
}
