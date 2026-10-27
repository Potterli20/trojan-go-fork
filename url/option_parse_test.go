package url

import (
	"strings"
	"testing"
)

// -url-option 的解析过去对分隔符不对称：尾随一个 ';' 就让 Cut 拿不到 '='，
// 于是 main 走 log.Fatal、一次完全可用的启动被判死；反过来未知键无声收下，
// 把 listne= 这类拼错静默丢掉，用户以为改了监听地址、实际还是默认值。
func TestParseOptionsToleratesEmptySegments(t *testing.T) {
	for _, in := range []string{
		"mux=true;",
		"mux=true;;listen=127.0.0.1:1080",
		";mux=true",
		"mux=true; ",
	} {
		got, err := parseOptions(in)
		if err != nil {
			t.Errorf("parseOptions(%q) 报错了：%v", in, err)
			continue
		}
		if got["mux"] != "true" {
			t.Errorf("parseOptions(%q) 丢了 mux：%v", in, got)
		}
	}
}

func TestParseOptionsRejectsUnknownKeys(t *testing.T) {
	for _, in := range []string{"listne=127.0.0.1:1080", "muxx=true", "plugin=obfs", "ap=127.0.0.1:0"} {
		if _, err := parseOptions(in); err == nil {
			t.Errorf("%q 被静默收下：拼错的键等于没写，但用户以为生效了", in)
		} else if !strings.Contains(err.Error(), "unknown") {
			t.Errorf("%q 的报错没有指向未知键：%v", in, err)
		}
	}
}

func TestParseOptionsKeepsKnownKeysAndMalformedDetection(t *testing.T) {
	got, err := parseOptions("mux=false;listen=127.0.0.1:8080;api=127.0.0.1:0")
	if err != nil {
		t.Fatalf("合法选项报错：%v", err)
	}
	if got["mux"] != "false" || got["listen"] != "127.0.0.1:8080" || got["api"] != "127.0.0.1:0" {
		t.Fatalf("选项解析结果不对：%v", got)
	}
	if _, err := parseOptions("mux"); err == nil {
		t.Fatal("缺 '=' 的片段必须报错")
	}
	if m, err := parseOptions(""); err != nil || len(m) != 0 {
		t.Fatal("空串应当返回空表且无错误")
	}
}
