package log

import (
	"strings"
	"testing"
)

const (
	hash56   = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678abcdef0123456789ab"
	run40    = "abcdefghijklmnopqrstuvwxyz0123456789WXYZ"
	jwtToken = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9" +
		".eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvIExvbiJ9" +
		".dtC1YDlUdE3zqC4mD7pLd0Tov7b8VzQm3rJ1kZ2xYqw"
)

// TestCredentialsAreStillRemoved 是安全性下限：改成"只遮命中片段"之后，
// 凭据本身一个字符都不能留下。
func TestCredentialsAreStillRemoved(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		leaks []string
	}{
		{"裸 56 位 SHA224（trojan 的线上凭据等价物）", "trojan: invalid hash " + hash56, []string{hash56, hash56[:40], hash56[40:]}},
		{"40 位以上十六进制串", "geodata checksum " + hash56[:44] + " mismatch", []string{hash56[:40], hash56[40:44]}},
		{"JWT 的 payload 段", "rejected jwt " + jwtToken, []string{"eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvIExvbiJ9", "dtC1YDlUdE3zqC4mD7pLd0Tov7b8VzQm3rJ1kZ2xYqw", jwtToken}},
		{"key=value 形态", "load config password=hunter2failed", []string{"hunter2failed"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ObfuscateSensitiveData(c.in)
			for _, leak := range c.leaks {
				if strings.Contains(got, leak) {
					t.Fatalf("凭据片段仍然可见：%q（原串 %q）", got, c.in)
				}
			}
		})
	}
}

// TestContextSurvivesRedaction 是这次改动的目的：非凭据的上下文必须留下。
// 旧实现对任何命中 32+ 位字母数字的行整行打码，运维看到的就是一个
// [REDACTED]，把唯一能用来定位的信息也一起丢了。
func TestContextSurvivesRedaction(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		mustHas []string
	}{
		{
			name:    "长 URL 路径只遮掉那段",
			in:      "tls failed to handshake with https://cdn.example.com/" + run40,
			mustHas: []string{"tls failed to handshake", "https://cdn.example.com/", "[REDACTED]"},
		},
		{
			name:    "trace id 前后的文字保留",
			in:      "worker " + hash56[:32] + " finished after 12ms",
			mustHas: []string{"worker", "finished after 12ms"},
		},
		{
			name:    "哈希被遮但错误身份还在",
			in:      "trojan: invalid hash " + hash56,
			mustHas: []string{"trojan: invalid hash", "[REDACTED]"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ObfuscateSensitiveData(c.in)
			for _, want := range c.mustHas {
				if !strings.Contains(got, want) {
					t.Fatalf("上下文丢了：缺 %q，实际 %q（原串 %q）", want, got, c.in)
				}
			}
			if got == redactedPlaceholder {
				t.Fatalf("整行仍被打码（%q）：判据要的是只遮命中片段", got)
			}
		})
	}
}

// TestKeywordHitStillRedactsWholeLine 钉住裁定：命中敏感关键词的行仍然整行打码，
// 因为那种行一般是配置转储，逐段遮反而容易漏。
func TestKeywordHitStillRedactsWholeLine(t *testing.T) {
	for _, in := range []string{
		"generated config password=short",
		"refresh token expired at 12",
		"load private-key from disk",
	} {
		if got := ObfuscateSensitiveData(in); got != redactedPlaceholder {
			t.Fatalf("含关键词的行应当整行打码，实际 %q", got)
		}
	}
}

// TestOrdinaryMessagesAreUntouched 是反向护栏：普通日志不该被动到。
func TestOrdinaryMessagesAreUntouched(t *testing.T) {
	for _, in := range []string{
		"failed to unmarshal config | invalid character at offset 12",
		"http failed to read request from local | unexpected EOF",
		"geodata file /var/lib/geodata/geoip.dat loaded",
		"mux client dead, 3 streams left",
	} {
		if got := ObfuscateSensitiveData(in); got != in {
			t.Fatalf("普通日志被改坏：%q -> %q", in, got)
		}
	}
}

// TestRedactedPlaceholderIsNotRematched 防的是自替换后再匹配：
// 替换产物本身不能再被后续规则吃掉（否则一行会越遮越多）。
func TestRedactedPlaceholderIsNotRematched(t *testing.T) {
	// 前缀里不能出现敏感关键词，否则会先走整行打码那条支路
	got := ObfuscateSensitiveData("trace-prefix " + run40 + " middle " + hash56 + " suffix")
	if n := strings.Count(got, redactedPlaceholder); n != 2 {
		t.Fatalf("两处凭据应当各遮一次，实际 %d 次：%q", n, got)
	}
	if !strings.Contains(got, "trace-prefix") || !strings.Contains(got, "middle") || !strings.Contains(got, "suffix") {
		t.Fatalf("替换产物把上下文一起吃掉了：%q", got)
	}
}
