package freedom

import (
	"context"
	"strings"
	"testing"

	"github.com/Potterli20/trojan-go-fork/config"
)

// TestForwardProxyEnabledRequiresAddress 钉住 forward_proxy 的启动期校验：
// enabled=true 却没给 proxy_addr / proxy_port 时，旧实现拼出 ":0"，NewClient
// 照样"成功"，直到第一次拨号才报 "failed to dial target address via socks
// proxy"——用户以为上游代理已启用，实际全程直连都没有（连 :0 也连不上），
// 且启动期零告警。口径同 shadowsocks/quic 的 remote-addr 闸、quic brutal：
// 策略性拒绝要在 NewClient 同步返回，不能拖到运行期静默失败。
//
// 变异对照：去掉 NewClient 里的 forward_proxy 地址闸，两条用例立刻转红。
func TestForwardProxyEnabledRequiresAddress(t *testing.T) {
	cases := []struct {
		name string
		blob string
	}{
		{"缺 proxy_addr", `{"forward_proxy":{"enabled":true,"proxy_port":1080}}`},
		{"缺 proxy_port", `{"forward_proxy":{"enabled":true,"proxy_addr":"127.0.0.1"}}`},
		{"只写 enabled", `{"forward_proxy":{"enabled":true}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, err := config.WithJSONConfig(context.Background(), []byte(tc.blob))
			if err != nil {
				t.Fatalf("加载配置失败：%v", err)
			}
			_, err = NewClient(ctx, nil)
			if err == nil {
				t.Fatal("forward_proxy.enabled=true 但地址不完整，NewClient 必须报错")
			}
			if !strings.Contains(err.Error(), "forward_proxy") {
				t.Fatalf("错误文案应指向 forward_proxy，实际：%v", err)
			}
		})
	}

	// 正向对照：没启用时不该被这道闸拦住（默认关闭时 proxy_addr/port 都是空）。
	ctx, err := config.WithJSONConfig(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("加载配置失败：%v", err)
	}
	c, err := NewClient(ctx, nil)
	if err != nil {
		t.Fatalf("未启用 forward_proxy 时 NewClient 不该报错：%v", err)
	}
	c.Close() //gosec:disable -- 清理客户端 ctx
}
