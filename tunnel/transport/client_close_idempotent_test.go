package transport

import (
	"context"
	"os/exec"
	"sync"
	"testing"
)

// TestConcurrentClientCloseIsIdempotent 钉住 Client.Close 的并发安全：
// transport 插件是一个 exec.Cmd，stdlib 明确规定 Cmd.Wait 不允许被多个 goroutine
// 并发调用（"Wait must not be called concurrently from multiple goroutines"）。
// 库化调用方（反复建栈/热重载）对同一 Client 并发 Close 时，旧实现每个 Close
// 都会 Process.Kill + cmd.Wait，撞上就是 data race，还伴重复 Kill。
// proxy/stack.go:147 注释明确"重复关闭由各 tunnel 的幂等 Close 兜住"，
// Server 侧已有 closeMu，Client 侧是漏网。
//
// 变异对照：去掉 closeOnce 幂等闸，本用例在 -race 下立刻报 Cmd.Wait 的 data race。
func TestConcurrentClientCloseIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// 用一个真实存活的子进程作插件：只有 Process != nil 时 Close 才会走 Wait 分支。
	cmd := exec.Command("sh", "-c", "sleep 30") //gosec:disable -- 固定参数的测试夹具
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动测试子进程失败：%v", err)
	}
	c := &Client{ctx: ctx, cancel: cancel, cmd: cmd}

	const n = 8
	var start sync.WaitGroup
	start.Add(1)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start.Wait()
			if err := c.Close(); err != nil { //gosec:disable -- Close 恒返回 nil
				t.Errorf("Close 返回错误：%v", err)
			}
		}()
	}
	start.Done()
	wg.Wait()
}
