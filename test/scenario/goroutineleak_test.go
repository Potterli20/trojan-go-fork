package scenario

import (
	"bytes"
	"fmt"
	"regexp"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
)

// leakStacks 取一次 Go 1.27 goroutineleak 剖析，返回"泄露协程"的栈指纹与数量。
//
// 为什么用它而不是 runtime.NumGoroutine：真实栈上"在途会话"和"泄露"在计数上完全
// 一样，只有这个剖析会先跑一轮泄露检测 GC，把"阻塞在 channel/mutex 上、且没有任何
// 活对象引得到"的协程单独点名（src/runtime/pprof/pprof.go:194,265）。
//
// debug=1 的输出是按栈聚合的：
//
//	goroutineleak profile: total 1
//	3 @ 0x48c9ca 0x419ff9 ...
//	#	0x6f96fb	github.com/.../diag.TestX.func1+0x1b	/path/file.go:12
//
// 指纹取「整条栈的函数名序列」，刻意去掉 pc、+0x 偏移与文件行号——那些每次编译都会
// 漂，拿它做差集等于给自己埋一条一改代码就红的用例。
func leakStacks(t *testing.T) map[string]int {
	t.Helper()
	prof := pprof.Lookup("goroutineleak")
	if prof == nil {
		t.Fatal("本机工具链没有注册 goroutineleak 剖析类型（需要 Go 1.27+）")
	}
	var buf bytes.Buffer
	if err := prof.WriteTo(&buf, 1); err != nil {
		t.Fatalf("写出 goroutineleak 剖析: %v", err)
	}

	out := map[string]int{}
	var frames []string
	count := 0
	inGroup := false
	flush := func() {
		if inGroup && len(frames) > 0 {
			out[strings.Join(frames, "<-")] += count
		}
		frames, count, inGroup = nil, 0, false
	}
	for raw := range strings.SplitSeq(buf.String(), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "" || strings.HasPrefix(line, "goroutineleak profile:"):
			continue
		case strings.HasPrefix(line, "goroutine "):
			// 逐协程文本格式（按版本可能走这条），当成一条计数为 1 的组
			flush()
			inGroup, count = true, 1
		case leakGroupHeader.MatchString(line):
			flush()
			inGroup = true
			count, _ = strconv.Atoi(leakGroupHeader.FindStringSubmatch(line)[1])
		case strings.HasPrefix(line, "#"):
			if name := frameName(line); name != "" {
				frames = append(frames, name)
			}
		}
	}
	flush()
	return out
}

// leakGroupHeader 匹配 "3 @ 0x.. 0x.." 这种聚合组头。
var leakGroupHeader = regexp.MustCompile(`^(\d+) @ `)

// frameName 从 "#\t0xdeadbeef\tpkg.Func+0x1b\t/path/file.go:12" 里只取 pkg.Func。
// 注意模块路径本身就带 "/"，所以只能按"字段以 / 开头是文件路径"来区分，
// 不能用"带斜杠就跳过"——那样每个函数名都会被丢掉。
func frameName(line string) string {
	for f := range strings.FieldsSeq(line) {
		if strings.HasPrefix(f, "#") || strings.HasPrefix(f, "0x") || strings.HasPrefix(f, "/") {
			continue
		}
		if !strings.Contains(f, ".") {
			continue
		}
		return strings.SplitN(f, "+", 2)[0]
	}
	return ""
}

// waitForLeakProfile 给会话拆除留一段有界时间；泄露检测本身要一轮 GC。
func waitForLeakProfile(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(200 * time.Millisecond)
	}
	runtime.GC()
}

// plantBlockedGoroutine 种一条"必然泄露"的阻塞协程。channel 走**参数副本**、
// 种完立刻返回：写成"局部变量再置 nil"的话，那个写和协程的读并发无同步，
// `-race`（nightly 的 race.yml）会直接把它报成数据竞争。
func plantBlockedGoroutine() {
	blocked := make(chan struct{})
	go func(c chan struct{}) {
		<-c // 永远等不到
	}(blocked)
}

// TestProbeIsNotVacuous 先自证判据能抓到泄露，否则后面那条用例只是"没报错而已"。
// 种的是最典型的一类：协程阻塞在一条再没人引用的 channel 上。
func TestProbeIsNotVacuous(t *testing.T) {
	plantBlockedGoroutine()
	runtime.GC()

	stacks := leakStacks(t)
	for fingerprint := range stacks {
		if strings.Contains(fingerprint, "plantBlockedGoroutine") {
			return
		}
	}
	t.Fatalf("种下的阻塞协程没有被 goroutineleak 抓到，判据不可信。当前指纹：%v", keysOf(stacks))
}

func keysOf(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestRealStackLeavesNoLeakedGoroutines 用 socks+shadowsocks+mux 的真实栈跑一轮流量，
// 关停后比对"泄露协程栈"的增量：任何新出现的、属于本仓的阻塞协程都算泄露。
//
// 用增量而不是绝对值：同一进程里先前其它用例的残留会被绝对判据重复计入，
// 那条用例会红得没有信息量。
func TestRealStackLeavesNoLeakedGoroutines(t *testing.T) {
	before := leakStacks(t)

	serverPort := common.PickPort("tcp", "127.0.0.1")
	socksPort := common.PickPort("tcp", "127.0.0.1")
	clientData := fmt.Sprintf(`
run-type: client
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: %d
password:
    - password
ssl:
    verify: false
    fingerprint: firefox
    sni: localhost
shadowsocks:
    enabled: true
    method: AEAD_CHACHA20_POLY1305
    password: 12345678
mux:
    enabled: true
`, socksPort, serverPort)
	serverData := fmt.Sprintf(`
run-type: server
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: 80
disable-http-check: true
password:
    - password
ssl:
    verify-hostname: false
    key: server.key
    cert: server.crt
    sni: localhost
shadowsocks:
    enabled: true
    method: AEAD_CHACHA20_POLY1305
    password: 12345678
`, serverPort)

	// 100 条并发连接走完整栈，然后由 CheckClientServer 关闭两侧 proxy
	if !CheckClientServer(clientData, serverData, socksPort) {
		t.Fatal("真实栈流量未通过，泄露比对没有意义")
	}
	waitForLeakProfile(t)

	after := leakStacks(t)
	var grew []string
	for fingerprint, n := range after {
		if delta := n - before[fingerprint]; delta > 0 {
			grew = append(grew, fmt.Sprintf("%d x %s", delta, fingerprint))
		}
	}
	if len(grew) > 0 {
		t.Fatalf("关停真实栈后新增 %d 类泄露协程：\n  %s", len(grew), strings.Join(grew, "\n  "))
	}
}
