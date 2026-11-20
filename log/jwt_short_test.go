package log

import (
	"strings"
	"testing"
)

// JWT 专用脱敏规则的 required 字面量必须与实际正则前缀一致，否则 ruleMayMatch
// 的预判恒为假、整条规则永不执行。旧实现 required 写成 "eyjhbggcioj"（正则
// 前缀 eyJhbGciOiJ 的小写是 "eyjhbgcioij"），两处手写副本漂移：短段 JWT
// （每段都不到 32 位、且整行没有 authorization/token 等关键词）会明文落进日志。
// 现有 JWT 用例三段都很长，被通用 32+ 规则遮挡，抓不到这条规则失效；本判据
// 用短段 JWT 直接钉行为，并加一条结构门禁把 required 与正则前缀绑在一起。
const shortJWT = "eyJhbGciOiJIUzI1NiJ9.e30.sig"

func TestShortJWTRedactedWithoutKeywordContext(t *testing.T) {
	in := "rejected jwt " + shortJWT
	got := ObfuscateSensitiveData(in)
	if strings.Contains(got, shortJWT) || strings.Contains(got, "eyJhbGciOiJ") {
		t.Fatalf("短 JWT 没有被脱敏，JWT 专用规则未生效（required 字面量与正则前缀不一致）: %q", got)
	}
	if !strings.Contains(got, "rejected jwt") {
		t.Fatalf("非凭据上下文应当保留，实际: %q", got)
	}
}

func TestJWTRuleRequiredMatchesRegexPrefix(t *testing.T) {
	// 行为判据的静态配套：required 必须能从正则的字面前缀派生，防止再次手写漂移。
	const prefix = "eyJhbGciOiJ"
	found := false
	for _, rule := range SensitivePatterns {
		if rule.re.String() != `eyJhbGciOiJ[a-zA-Z0-9_.=-]+` {
			continue
		}
		found = true
		if len(rule.required) != 1 || !strings.EqualFold(rule.required[0], strings.ToLower(prefix)) {
			t.Fatalf("JWT 规则的 required=%v 与正则前缀 %q 不一致：ruleMayMatch 预判会恒为假", rule.required, prefix)
		}
	}
	if !found {
		t.Fatal("找不到 JWT 专用脱敏规则：判据失效")
	}
}
