package custom

import (
	"strings"
	"testing"

	"github.com/Potterli20/trojan-go-fork/proxy"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/freedom"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/quic"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/tls"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/transport"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/trojan"
)

// 出站链只对首节点做了 clientCanBeInnermost 校验（最内层必须是自拨号层），
// 却没有入站 rootCanStandAlone 那道"自绑层不得位于非首节点"的对称闸。
// 自拨号层（FREEDOM/TRANSPORT/QUIC）的 NewClient 忽略传入的 underlay，
// 放在非最内层时内层节点被完整建好（ctx/TLS/全局出网状态/QUIC 资源），
// 实际流量却不经过它——内层配置静默失效、资源孤立，建栈仍成功、无日志。
//
// 判据：自拨号层出现在非最内层必须报错；合法的各层顺序仍成功（正向对照）。
func TestOutboundSelfDialingLayerMustBeInnermost(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "transport 在非最内层（freedom 才是最内层根）",
			path:    `[["f","tr","tls","tro"]]`,
			wantErr: true,
		},
		{
			name:    "freedom 在非最内层（transport 是更内层根）",
			path:    `[["tr","f","tls","tro"]]`,
			wantErr: true,
		},
		{
			name:    "quic 在非最内层",
			path:    `[["f","q","tls","tro"]]`,
			wantErr: true,
		},
		{
			name:    "自拨号层只在最内层（合法）",
			path:    `[["f","tls","tro"]]`,
			wantErr: false,
		},
	}
	const nodeDefs = `[
		{"protocol":"transport","tag":"tr","config":{"local_addr":"127.0.0.1","local_port":0,"remote_addr":"127.0.0.1","remote_port":0}},
		{"protocol":"freedom","tag":"f","config":{}},
		{"protocol":"quic","tag":"q","config":{"remote_addr":"127.0.0.1","remote_port":0}},
		{"protocol":"tls","tag":"tls","config":{"sni":"example.com"}},
		{"protocol":"trojan","tag":"tro","config":{"password":["0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"]}}
	]`
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := `{
				"run_type":"custom",
				"inbound":{
					"node":[{"protocol":"transport","tag":"in","config":{"local_addr":"127.0.0.1","local_port":0,"remote_addr":"127.0.0.1","remote_port":0}}],
					"path":[["in"]]
				},
				"outbound":{"node":` + nodeDefs + `,"path":` + c.path + `}
			}`
			p, err := proxy.NewProxyFromConfigData([]byte(cfg), true)
			if c.wantErr {
				if err == nil {
					if p != nil {
						p.Close() //gosec:disable -- 错误忽略：测试清理
					}
					t.Fatal("自拨号层出现在非最内层时建栈成功：内层配置被静默吞掉、资源孤立")
				}
				if !strings.Contains(err.Error(), "innermost") {
					t.Fatalf("错误应指出自拨号层必须是最内层：%v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("合法的自拨号首层链路应建栈成功：%v", err)
			}
			if p != nil {
				p.Close() //gosec:disable -- 错误忽略：测试清理
			}
		})
	}
}
