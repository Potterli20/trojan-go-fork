package socks

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// udp-timeout 写成 0 或负数时必须落回文档默认值。time.NewTimer(0) 立即就绪，会话守护
// 协程首轮 select 必走 timer 分支 —— 每条 UDP 会话在建立的那一刻就被删表 + Close，
// UDP 静默全废，而日志只有 Info 一行。同型的 mux idle-timeout 缺陷已按"非正数落
// 文档默认值"修过（tunnel/mux/idle_timeout_test.go）。
func TestResolveUDPTimeoutClampsNonPositive(t *testing.T) {
	for _, seconds := range []int{0, -1, -600} {
		if got := resolveUDPTimeout(seconds); got != udpDefaultTimeout {
			t.Errorf("UDPTimeout=%d 换算成 %v，期望兜底 %v", seconds, got, udpDefaultTimeout)
		}
	}
	if got := resolveUDPTimeout(30); got != 30*time.Second {
		t.Errorf("UDPTimeout=30 应当是 30s，实际 %v", got)
	}
}

// 谓词有牙不等于接线有牙：夹紧必须真的用在服务构造点上。
// 这条结构性门禁抓的是"把 timeout 退回裸 time.Duration(cfg.UDPTimeout)"。
func TestServerBuildsItsTimeoutThroughTheClamp(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("拿不到本文件名，门禁失效")
	}
	src, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "server.go"))
	if err != nil {
		t.Fatalf("读 server.go: %v", err)
	}
	text := string(src)
	if !strings.Contains(text, "resolveUDPTimeout(cfg.UDPTimeout)") {
		t.Fatal("server.go 里的会话超时没有走 resolveUDPTimeout 夹紧")
	}
	if strings.Contains(text, "time.Duration(cfg.UDPTimeout)") {
		t.Fatal("server.go 里仍有裸的 time.Duration(cfg.UDPTimeout)：绕过了非正数夹紧")
	}
}
