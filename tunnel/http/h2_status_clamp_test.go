package http

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// 实测 stdlib 的 http.ReadResponse 会收下 "HTTP/1.1 000 x"（StatusCode=0）与
// "HTTP/1.1 099 x"（StatusCode=99），而 x/net/http2 的 checkWriteHeaderCode 对
// code < 100 直接 panic。判据的诚实边界写在 h2.go 的注释里：端到端断言在这里恒真，
// 所以钉的是纯函数 + 接线。
func TestClampOriginStatusCode(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, statusBadGateway}, // 实测 ReadResponse 会给出 0
		{1, statusBadGateway},
		{99, statusBadGateway}, // 实测 ReadResponse 会给出 99
		{100, 100},             // informational 由 x/net 自己处理，不钳
		{200, 200},
		{502, 502},
		{999, 999}, // 三位上界仍合法（stdlib 只按 >=0 收）
	}
	for _, c := range cases {
		if got := clampOriginStatusCode(c.in); got != c.want {
			t.Errorf("clampOriginStatusCode(%d) = %d，期望 %d", c.in, got, c.want)
		}
	}
}

// statusBadGateway 用字面量，避免把被测函数和 stdlib 常量绑成同一个名字。
const statusBadGateway = 502

// 谓词有牙不等于接线有牙：写出响应头的那一站必须真的经过它。
func TestOriginStatusIsClampedOnTheWriteHeaderSite(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("拿不到本文件名，门禁失效")
	}
	src, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "h2.go"))
	if err != nil {
		t.Fatalf("读 h2.go: %v", err)
	}
	text := string(src)
	if !strings.Contains(text, "status := clampOriginStatusCode(resp.StatusCode)") ||
		!strings.Contains(text, "w.WriteHeader(status)") {
		t.Fatal("h2.go 里写出状态码的那一站没有接上 clampOriginStatusCode")
	}
	if strings.Contains(text, "w.WriteHeader(resp.StatusCode)") {
		t.Fatal("h2.go 仍在把未经钳制的原始状态码直接交给 WriteHeader")
	}
}
