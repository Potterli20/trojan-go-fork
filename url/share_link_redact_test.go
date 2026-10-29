package url

import (
	"strings"
	"testing"
)

// net/url 的解析错误会把**整个输入**回显进错误串（实测：
// parse "trojan-go://shortpw@example.com:443/%zz": invalid URL escape "%zz"），
// 而这条错误经 url/option.go 的 common.NewError("failed to parse URL").Base(err)
// 冒到 main.go 打日志——于是 -url 打错一次就在日志里明文交出一个密码。
//
// 兜底规则救不了这一类：8 位密码既不含 password/token 这类关键词，也够不着
// 32 位连续字母数字的阈值，一路明文。所以这道必须在结构性上不嵌原文。
//
// 正向对照：把 share_link.go 里的 maskURLUserinfo(e.Error()) 退回 e.Error()，
// 这三条用例全部变红。
func TestInvalidURLErrorDoesNotEchoPassword(t *testing.T) {
	const password = "shortpw1"
	_, err := NewShareInfoFromURL("trojan-go://" + password + "@example.com:443/%zz")
	if err == nil {
		t.Fatal("坏转义必须解析失败")
	}
	if strings.Contains(err.Error(), password) {
		t.Fatalf("分享链接里的密码被回显进错误文案：%q", err.Error())
	}
}

// 诊断信息不能因为脱敏一起丢掉：坏在哪个转义、哪台主机，运维还得看得见。
func TestInvalidURLErrorKeepsDiagnostics(t *testing.T) {
	_, err := NewShareInfoFromURL("trojan-go://shortpw1@example.com:443/%zz")
	if err == nil {
		t.Fatal("坏转义必须解析失败")
	}
	msg := err.Error()
	for _, want := range []string{"invalid URL escape", "%zz", "example.com", "443"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("错误文案丢了诊断片段 %q：%q", want, msg)
		}
	}
	if !strings.Contains(msg, "[REDACTED]@") {
		t.Fatalf("userinfo 没有被占位符替换，看不出遮在哪儿：%q", msg)
	}
}

// user:password 两段都在 userinfo 里，必须一起遮掉；只遮前缀等于没遮。
func TestInvalidURLErrorMasksBothUserinfoParts(t *testing.T) {
	const secret = "topsecret9"
	_, err := NewShareInfoFromURL("trojan-go://u1:" + secret + "@example.com:443/%zz")
	if err == nil {
		t.Fatal("坏转义必须解析失败")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("冒号后的口令段漏出来了：%q", err.Error())
	}
}
