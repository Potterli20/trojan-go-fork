package router

import (
	"context"
	"net"
	"strings"
	"testing"

	v2geodata "github.com/xtls/xray-core/common/geodata"

	"github.com/Potterli20/trojan-go-fork/config"
)

// 前缀位数越界的 CIDR 过去能被静默接受：net.CIDRMask 对越界位数返回 nil，规则恒不命中，
// 流量落到 default_policy。写 block: cidr:1.2.3.0/33 的人以为自己在拦，实际是放行——
// 而默认策略是 proxy，所以这条静默失效的方向恰好是"悄悄放行"。现在装载期就报错。
func TestNewClientRejectsOutOfRangeCIDRPrefix(t *testing.T) {
	for _, code := range []string{
		"cidr:1.2.3.0/33",     // IPv4 只有 0..32
		"cidr:1.2.3.0/-1",     // ParseInt 允许负数，转 uint32 后回绕成天文数字
		"cidr:2001:db8::/129", // IPv6 只有 0..128
	} {
		ctx := routerConfig(t, code)
		_, err := NewClient(ctx, &MockClient{})
		if err == nil {
			t.Errorf("%q 被静默接受：这条规则永远不会命中，而错误只会在流量被放行之后才显形", code)
			continue
		}
		if !strings.Contains(err.Error(), "cidr") {
			t.Errorf("%q 的报错没有指向出错的规则：%v", code, err)
		}
	}
}

// 正向对照：合法前缀必须照旧接受，否则上面那条门禁只是在拒绝一切。
func TestNewClientStillAcceptsValidCIDRPrefix(t *testing.T) {
	ctx := routerConfig(t,
		"cidr:1.2.3.0/24", "cidr:2001:db8::/64", "cidr:1.2.3.4/32", "cidr:0.0.0.0/0")
	if _, err := NewClient(ctx, &MockClient{}); err != nil {
		t.Fatalf("合法前缀被拒：%v", err)
	}
}

func TestMatchIPNeverMatchesAnOutOfRangePrefix(t *testing.T) {
	// 装载侧拦过之后，运行期表里不该再有越界条目；matchIP 的 nil 掩码分支是结构兜底，
	// 它必须"不命中且不崩"，而不是拿一个恒假的 IPNet 去比。
	target := net.ParseIP("1.2.3.4").To4()
	list := []*v2geodata.CIDR{{Ip: net.IPv4(1, 2, 3, 0).To4(), Prefix: 33}}
	if matchIP(list, target) {
		t.Fatal("越界前缀被判成命中：掩码分支写错了")
	}
	// 正向对照：同一张表换成合法前缀必须命中，否则上面那条断言不构成判据。
	if !matchIP([]*v2geodata.CIDR{{Ip: net.IPv4(1, 2, 3, 0).To4(), Prefix: 24}}, target) {
		t.Fatal("/24 应当命中 1.2.3.4：对照失效")
	}
}

func routerConfig(t *testing.T, cidrs ...string) context.Context {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("router:\n    enabled: true\n    block:\n")
	for _, c := range cidrs {
		sb.WriteString("    - \"" + strings.ReplaceAll(c, `"`, `\"`) + "\"\n")
	}
	sb.WriteString("    default_policy: \"direct\"\n")
	ctx, err := config.WithYAMLConfig(context.Background(), []byte(sb.String()))
	if err != nil {
		t.Fatalf("构造 router 配置: %v", err)
	}
	return ctx
}
