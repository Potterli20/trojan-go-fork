package service

import (
	"context"
	"testing"

	"github.com/Potterli20/trojan-go-fork/api"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/tunnel/trojan"
)

// TestCheckAPIExposureFailsClosed 钉住"失败关闭"这条策略本身。
//
// gRPC API 能增删用户、尾随流量记录，而它自己不带任何鉴权；`api_addr` 留空时
// `net.ResolveIPAddr("ip", "")` 给出通配地址，等于开到所有网卡上（docs/configuration.md
// 的第 1 条实测）。所以：非回环监听 + 没有任何凭据 = 拒绝启动，而不是打一行日志继续跑。
// 逃生阀是显式的 `api.allow_insecure`，不能是默认。
func TestCheckAPIExposureFailsClosed(t *testing.T) {
	cases := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{
			name:    "api 未启用时不管监听地址",
			cfg:     &Config{API: APIConfig{Enabled: false, APIHost: "0.0.0.0"}},
			wantErr: false,
		},
		{
			name:    "回环 127.0.0.1 无凭据也放行",
			cfg:     &Config{API: APIConfig{Enabled: true, APIHost: "127.0.0.1"}},
			wantErr: false,
		},
		{
			name:    "回环 ::1 无凭据也放行",
			cfg:     &Config{API: APIConfig{Enabled: true, APIHost: "::1"}},
			wantErr: false,
		},
		{
			name:    "api_addr 留空 = 所有网卡，无凭据必须拒",
			cfg:     &Config{API: APIConfig{Enabled: true}},
			wantErr: true,
		},
		{
			name:    "0.0.0.0 无凭据必须拒",
			cfg:     &Config{API: APIConfig{Enabled: true, APIHost: "0.0.0.0"}},
			wantErr: true,
		},
		{
			name:    "公网地址无凭据必须拒",
			cfg:     &Config{API: APIConfig{Enabled: true, APIHost: "203.0.113.7"}},
			wantErr: true,
		},
		{
			name:    "公网 + token 放行",
			cfg:     &Config{API: APIConfig{Enabled: true, APIHost: "203.0.113.7", Token: "s3cret"}},
			wantErr: false,
		},
		{
			name: "公网 + TLS 但不校验客户端证书仍然拒（加密不等于鉴权）",
			cfg: &Config{API: APIConfig{Enabled: true, APIHost: "203.0.113.7",
				SSL: SSLConfig{Enabled: true, VerifyClient: false}}},
			wantErr: true,
		},
		{
			name: "公网 + TLS + verify_client 放行",
			cfg: &Config{API: APIConfig{Enabled: true, APIHost: "203.0.113.7",
				SSL: SSLConfig{Enabled: true, VerifyClient: true}}},
			wantErr: false,
		},
		{
			name: "显式逃生阀 allow_insecure 放行",
			cfg: &Config{API: APIConfig{Enabled: true, APIHost: "203.0.113.7",
				AllowInsecure: true}},
			wantErr: false,
		},
		{
			name:    "监听地址解析不出来直接报错",
			cfg:     &Config{API: APIConfig{Enabled: true, APIHost: "not-an-ip"}},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkAPIExposure(tc.cfg)
			if tc.wantErr && err == nil {
				t.Fatal("应当拒绝启动，却放行了")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("不该拒绝，却报错了: %v", err)
			}
		})
	}
}

// TestExposureCheckerIsRegisteredForBothSides 钉住注册这一环：闸门只有在
// tunnel/trojan 的 NewServer/NewClient 能查到检查器时才有效，而那两个入口是靠
// name 字符串找它的（trojan.Name+"_SERVER" / +"_CLIENT"）。少注册一个，那一侧就
// 静默地回到"随便开"。
func TestExposureCheckerIsRegisteredForBothSides(t *testing.T) {
	for _, name := range []string{trojan.Name + "_SERVER", trojan.Name + "_CLIENT"} {
		ctx := config.WithConfig(context.Background(), Name, &Config{
			API: APIConfig{Enabled: true, APIHost: "203.0.113.7"},
		})
		if err := api.CheckExposure(ctx, name); err == nil {
			t.Fatalf("%s 的暴露面检查器没有生效：无凭据的公网监听被放行了", name)
		}
	}
	// 没登记过的 name 必须放行：mini 构建里没有 api/service，CheckExposure 不能变成硬失败
	if err := api.CheckExposure(context.Background(), "SOME_OTHER_SERVICE"); err != nil {
		t.Fatalf("未注册的 name 应当放行，实得 %v", err)
	}
}
