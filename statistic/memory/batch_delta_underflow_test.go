package memory

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// batchTrafficUpdater 的 deltaSent/deltaRecv 用裸 uint64 减法，而 ResetTraffic
// （User.Close 会调）与管理员 SetUserTraffic 都能把累计量调低：读基线到读当前值之间
// 一旦被重置，sent-lastSent 下溢成约 1.8e19，经日志聚合的 sentBytes/recvBytes 输出
// 完全失真的天文数字。同文件 speedUpdater 早已用 saturatingSub 防同一类下溢。
// 端到端窗口依赖两个 goroutine 的精确交错，难以稳定复现，照 h2_status_clamp 的先例
// 用结构门禁钉接线，并补 saturatingSub 自身的边界。
func TestBatchDeltaUsesSaturatingSub(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("拿不到本文件名，门禁失效")
	}
	src, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "memory.go"))
	if err != nil {
		t.Fatalf("读 memory.go: %v", err)
	}
	text := string(src)
	if !strings.Contains(text, "deltaSent := saturatingSub(sent, lastSent)") ||
		!strings.Contains(text, "deltaRecv := saturatingSub(recv, lastRecv)") {
		t.Fatal("batchTrafficUpdater 的增量没有走 saturatingSub：重置流量时会 uint64 下溢")
	}
	if strings.Contains(text, "deltaSent := sent - lastSent") || strings.Contains(text, "deltaRecv := recv - lastRecv") {
		t.Fatal("仍有裸 uint64 减法计算增量")
	}
}

func TestSaturatingSubClampsReset(t *testing.T) {
	cases := []struct{ cur, last, want uint64 }{
		{100, 40, 60},
		{40, 100, 0}, // 重置/调低：钳到 0，而不是 2^64-60
		{0, 0, 0},
	}
	for _, c := range cases {
		if got := saturatingSub(c.cur, c.last); got != c.want {
			t.Errorf("saturatingSub(%d,%d)=%d，期望 %d", c.cur, c.last, got, c.want)
		}
	}
}
