package diag

import (
	"bytes"
	"io"
	"net/http"
	"runtime"
	"runtime/pprof"
	"strings"
	"testing"
)

// plantBlockedGoroutine 种一条"必然泄露"的阻塞协程，用来自证判据不是假阴性机器。
//
// channel 必须以**参数副本**交给协程、且本函数真的返回：早先写成"局部变量置 nil"，
// 那个写与协程的读并发无同步，`-race` 会直接把它报成数据竞争（在 nightly 的 race.yml
// 里就是一条假警报）。
func plantBlockedGoroutine() {
	blocked := make(chan struct{})
	go func(c chan struct{}) {
		<-c // 永远等不到
	}(blocked)
}

// TestGoroutineLeakProfileDetectsPlantedLeak 先用一条**故意种下的泄露**自证工具可用。
//
// Go 1.27 的 goroutineleak 剖析（src/runtime/pprof/pprof.go:194,265）会先跑一轮带泄露
// 检测的 GC，然后只输出"阻塞在同步原语上、且没有任何活对象能引到它"的协程。不先证明
// 它认得出被丢弃的阻塞协程，后面所有基于它的判据都只是"没报错而已"，属于假阴性机器。
func TestGoroutineLeakProfileDetectsPlantedLeak(t *testing.T) {
	prof := pprof.Lookup("goroutineleak")
	if prof == nil {
		t.Fatal("本机工具链没有注册 goroutineleak 剖析类型")
	}

	plantBlockedGoroutine()
	runtime.GC()

	var buf bytes.Buffer
	// debug=1 才是可读文本；debug=0 是压缩的 profile 格式，既没法断言也没法在失败时看
	if err := prof.WriteTo(&buf, 1); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	text := buf.String()
	if !strings.Contains(text, "plantBlockedGoroutine") {
		t.Fatalf("种下的阻塞协程没有被 goroutineleak 认出来。剖析摘要：%s",
			firstLines(text, 12))
	}
	if prof.Count() < 1 {
		t.Fatalf("goroutineleak 认出了栈但计数为 %d", prof.Count())
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// TestStartRefusesNonLoopback 是安全边界：诊断口能读协程栈、堆直方图，还能开持续
// 占用 CPU 的 profile 采集，挂到非回环地址等于把进程交出去。
func TestStartRefusesNonLoopback(t *testing.T) {
	for _, bind := range []string{":0", "0.0.0.0:0", "0.0.0.0:6060"} {
		_, stop, err := Start(bind)
		if err == nil {
			stop()
			t.Fatalf("绑定 %q 竟然通过了回环校验", bind)
		}
		if !strings.Contains(err.Error(), "loopback") {
			t.Fatalf("绑定 %q 的拒绝原因不对：%v", bind, err)
		}
	}
}

// TestStartServesGoroutineLeakOnLoopback 端到端走一遍 HTTP：确认这个新剖析类型
// 真的能从 /debug/pprof/ 下拿到，而不是只在进程内可用。
func TestStartServesGoroutineLeakOnLoopback(t *testing.T) {
	addr, stop, err := Start("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer stop()

	resp, err := http.Get("http://" + addr + "/debug/pprof/goroutineleak?debug=1")
	if err != nil {
		t.Fatalf("GET goroutineleak: %v", err)
	}
	defer resp.Body.Close() //gosec:disable -- 错误忽略：测试清理
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d，期望 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应: %v", err)
	}
	if !strings.Contains(string(body), "goroutineleak profile") {
		t.Fatalf("响应里没有 goroutineleak 剖析：%s", firstLines(string(body), 8))
	}
}
