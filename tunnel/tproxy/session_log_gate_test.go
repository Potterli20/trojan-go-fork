package tproxy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// 建会话 Info 的实参 info.dst.String() 是每会话一次的 IP 格式化：级别不够时必须在
// 门禁之后求值，否则是纯白付（AGENTS.md 口径：「日志调用点先判级别，再动昂贵实参」；
// 同函数的拒绝分支早已限流，这里是它不对称的另一半）。
// 这条做不出稳定的端到端判据——要捕获全局 logger 且与同包并发用例互踩——
// 照 h2_status_clamp_test.go 的先例用结构门禁：同时钉「门禁先行」与「日志仍在」。
func TestUDPSessionCreationLogIsGatedByShouldLog(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("拿不到本文件名，门禁失效")
	}
	src, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "server.go"))
	if err != nil {
		t.Fatalf("读 server.go: %v", err)
	}
	lines := strings.Split(string(src), "\n")
	logIdx := -1
	for i, line := range lines {
		if strings.Contains(line, `log.Info("new tproxy udp session from"`) {
			logIdx = i
			break
		}
	}
	if logIdx < 0 {
		t.Fatal("找不到建会话日志调用点：判据失效，别把日志整个删掉")
	}
	window := strings.Join(lines[max(0, logIdx-3):logIdx], "\n")
	if !strings.Contains(window, "if log.ShouldLog(log.InfoLevel) {") {
		t.Fatal("建会话日志没有先过 ShouldLog 门禁：info.dst.String() 会在级别不足时白付一次 IP 格式化")
	}
}
