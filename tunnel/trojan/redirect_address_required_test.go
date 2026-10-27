package trojan

import (
	"context"
	"strings"
	"testing"

	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/statistic/memory"
)

func serverCtx(cfg *Config) context.Context {
	ctx := config.WithConfig(context.Background(), Name, cfg)
	return config.WithConfig(ctx, memory.Name, &memory.Config{Passwords: []string{"pw"}})
}

// 诱饵地址缺主机名时必须拒绝启动。NewAddressFromHostPort("tcp", "", port) 的 String()
// 是 ":port"，而实测 net.Dial("tcp", ":port") 连的是 **127.0.0.1**——认证失败的流量会被
// 代打到代理自己主机上的本地服务。disable-http-check: true 时 NewServer 里那道探测不跑，
// 这条路径就完全是静默的。姊妹入站早就拦了（shadowsocks/quic 显式拒绝空 RemoteHost）。
func TestNewServerRequiresRedirectionAddress(t *testing.T) {
	Auth = nil
	cfg := &Config{RemoteHost: "", RemotePort: 8443, DisableHTTPCheck: true}
	_, err := NewServer(serverCtx(cfg), nil)
	if err == nil {
		t.Fatal("remote-addr 为空时 NewServer 应当失败，而不是造出一个会连到 127.0.0.1 的诱饵地址")
	}
	if !strings.Contains(err.Error(), "remote-addr") {
		t.Fatalf("报错必须指向 remote-addr 这一项，实际：%v", err)
	}
}

// 正向对照：给了主机名就不该被这道闸拦住（下面这一步只关心"错误不是空地址那条"）。
func TestNewServerAcceptsExplicitRedirectionAddress(t *testing.T) {
	Auth = nil
	cfg := &Config{RemoteHost: "127.0.0.1", RemotePort: 1, DisableHTTPCheck: true}
	_, err := NewServer(serverCtx(cfg), nil)
	if err != nil && strings.Contains(err.Error(), "remote-addr is empty") {
		t.Fatalf("显式给了 remote-addr 却被空地址闸门拒掉：%v", err)
	}
}
