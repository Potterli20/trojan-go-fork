package socks

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// SOCKS UDP 短包（n<4）丢弃分支的 log.Debug 实参 src.String() 在调用前就求值，
// 而 log.Debug 的 ShouldLog 门禁在函数体内——默认 Info 级别下每丢一个短包仍白付
// 一次 net.Addr.String()（IP 格式化 + 分配）。对端是未认证、可伪造源的 UDP，
// 这是把日志实参放大成攻击面。同函数 parseErrors 与 limitDrops 两分支都已把
// src.String() 放到 ShouldLog 之后，唯独这条漏网。
// 端到端判据要捕获全局 logger、还会与同包并发用例互踩，照既有先例用结构门禁。
func TestShortUDPPacketLogIsGatedByShouldLog(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("拿不到本文件名，门禁失效")
	}
	src, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "server.go"))
	if err != nil {
		t.Fatalf("读 server.go: %v", err)
	}
	lines := strings.Split(string(src), "\n")
	idx := -1
	for i, line := range lines {
		if strings.Contains(line, `log.Debug("socks dropping short udp packet from"`) {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("找不到短包丢弃日志调用点：判据失效，别把日志整个删掉")
	}
	window := strings.Join(lines[max(0, idx-2):idx+1], "\n")
	if !strings.Contains(window, "log.ShouldLog(log.DebugLevel)") {
		t.Fatal("短包丢弃日志没有先过 ShouldLog：src.String() 在级别不足时白付格式化")
	}
	// 分母自证：同函数另两条姊妹分支确实带门禁，避免上面读空窗口误判通过。
	whole := string(src)
	if !strings.Contains(whole, "if log.ShouldLog(log.DebugLevel)") {
		t.Fatal("同文件找不到 ShouldLog 用法：门禁文本匹配失效")
	}
}
