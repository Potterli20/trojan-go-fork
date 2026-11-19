package fingerprint

import (
	"strings"
	"testing"
)

// cipher 名字拼错过去被 ParseCipher 静默丢弃：配了非空 cipher 却解析出 0 个套件，
// tls.Config.CipherSuites 拿到 nil 就走 Go 默认全套——用户以为限制了加密套件，
// 实际完全没限制，握手照常成功、无任何症状。对照已修的 curve_preferences：
// 启动期必须拒绝未知名字。
func TestValidateCipherRejectsUnknownNames(t *testing.T) {
	for _, name := range []string{"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256_TYPO", "tls_foo", "not-a-cipher"} {
		err := ValidateCipher([]string{name})
		if err == nil {
			t.Errorf("%q 被静默接受：配错 cipher 名字必须启动期报错", name)
			continue
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("%q 的报错没有指名道姓：%v", name, err)
		}
	}
}

// 合法名字（含 TLS 1.3 套件）必须放行，且解析出的 ID 与校验一致。
func TestValidateCipherAcceptsKnownNames(t *testing.T) {
	names := []string{"TLS_AES_128_GCM_SHA256", "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256", "TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384"}
	if err := ValidateCipher(names); err != nil {
		t.Fatalf("合法 cipher 名字被拒：%v", err)
	}
	ids := ParseCipher(names)
	if len(ids) != len(names) {
		t.Fatalf("解析结果有丢失：得到 %d 个，期望 %d 个", len(ids), len(names))
	}
}

// 空项（配置 "a::b" 或前后空格）按 curve_preferences 的既有口径跳过，不当成未知名字。
func TestValidateCipherSkipsEmptyEntries(t *testing.T) {
	if err := ValidateCipher([]string{"", "  ", "TLS_AES_128_GCM_SHA256"}); err != nil {
		t.Fatalf("空项应当跳过：%v", err)
	}
}

// 校验与解析必须同口径：ValidateCipher 对名字 TrimSpace，ParseCipher 也必须 TrimSpace。
// 配置里 "cipher1: cipher2" 这种带空格的写法很常见；若校验放行而解析时不 trim，
// 名字会被静默丢弃——正是本轮要根除的"接受但不生效"。curve 的两侧都 trim，cipher 对齐。
func TestParseCipherTrimsLikeValidation(t *testing.T) {
	names := []string{"  TLS_AES_128_GCM_SHA256  ", " TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"}
	if err := ValidateCipher(names); err != nil {
		t.Fatalf("带空格的合法名字应通过校验：%v", err)
	}
	ids := ParseCipher(names)
	if len(ids) != len(names) {
		t.Fatalf("校验放行但解析丢弃：得到 %d 个套件，期望 %d 个——带空格的名字没被 trim",
			len(ids), len(names))
	}
}
