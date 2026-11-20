//go:build linux

package tproxy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Go 1.23 起 time.Timer 的通道改成同步（无缓冲），Stop/Reset 自带"清除旧值"语义，
// 旧写法 `if !timer.Stop() { <-timer.C }; timer.Reset(...)` 的排空分支在 1.23+
// 永不命中（timer 运行中且未被接收时 Stop 恒返回 true；唯一接收者收到即 return），
// 是纯死代码，还会误导读者以为这里需要手动 drain。
//
// 这是清理死代码的结构门禁：三处 UDP 会话守护协程必须直接 timer.Reset，
// 不再保留 Stop+drain 模式。行为正确性由既有的 udp_session_timer 路径覆盖判据保证。
func TestUDPSessionTimersDoNotUseLegacyStopDrain(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("拿不到本文件名，门禁失效")
	}
	files := []string{
		filepath.Join(filepath.Dir(thisFile), "server.go"),
		filepath.Join(filepath.Dir(thisFile), "..", "socks", "server.go"),
		filepath.Join(filepath.Dir(thisFile), "..", "dokodemo", "server.go"),
	}
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读 %s: %v", path, err)
		}
		lines := strings.Split(string(src), "\n")
		foundLegacy := false
		for i, line := range lines {
			if strings.Contains(line, "if !timer.Stop()") {
				// 往后几行里若出现 <-timer.C，就是 Go 1.23 之前的遗留排空模式。
				end := min(i+4, len(lines))
				for _, follow := range lines[i+1 : end] {
					if strings.Contains(follow, "<-timer.C") {
						foundLegacy = true
						break
					}
				}
			}
		}
		if foundLegacy {
			t.Fatalf("%s 仍在使用 Go 1.23 之前的 timer Stop+drain 遗留模式（死代码）；应直接 timer.Reset", path)
		}
	}
}
