package proxy

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"

	// 必须空导入挂上真实 logger：log 包默认是 EmptyLogger，不挂的话任何 tag 下都
	// 一行都写不出来，"stderr 是空的" 会被当成 bug 命中，判据反过来是假的。
	// 被测不变量是"输出指向谁"，与用哪个 logger 实现无关。
	_ "github.com/Potterli20/trojan-go-fork/log/golog"
)

// closeStub 是一个只为了「让 Close 带回一个错误」的出站桩。Close 的错误会被
// RunAndClose 合并上抛，而 main 是在这之后才把原因写出来的——正好是要守的那条路径。
type closeStub struct{}

func (s *closeStub) DialConn(*tunnel.Address, tunnel.Tunnel) (tunnel.Conn, error) {
	return nil, common.NewError("stub does not dial")
}

func (s *closeStub) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, common.NewError("stub does not dial")
}

func (s *closeStub) Close() error {
	return common.NewError("stub close failed")
}

// detachTestEnv 把 os.Stderr 换成一个临时文件，并把 log 的输出先彻底移出 stderr。
// 两步都省不掉：不接管 stderr 就看不见「交还」是否发生；不先移走，输出里残留的旧内容
// 会让未修复版本也「看见标记」，判据就废了。
func detachTestEnv(t *testing.T) *os.File {
	t.Helper()

	capture, err := os.CreateTemp(t.TempDir(), "stderr-*")
	if err != nil {
		t.Fatal(err)
	}
	realStderr := os.Stderr
	previousLevel := log.GetLogLevel()
	os.Stderr = capture
	log.SetOutput(io.Discard)

	t.Cleanup(func() {
		os.Stderr = realStderr
		log.SetLogLevel(previousLevel)
		log.SetOutput(realStderr)
		capture.Close()
	})
	return capture
}

func readCaptured(t *testing.T, capture *os.File) string {
	t.Helper()
	if err := capture.Sync(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(capture.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestNewProxyFailureLeavesLogOutputUsable 盯住启动失败时错误到底报不报得出来。
//
// NewProxyFromConfigData 的顺序是先 log.SetOutput(logFile)、再 create(ctx)。create 失败
// 时如果只把文件 Close 掉而不把输出交还 stderr，之后 main 写的 WARN/ERROR/FATAL 全都
// 落进一个已经关闭的 *os.File：它不是 nil，判空短路不掉；写入得到 "file already
// closed"，而日志包不检查写错误，于是整行静默消失。
//
// 实测后果（配 log-file + 诱饵地址没起）：退出码 1，stdout / stderr / 日志文件全是
// 0 字节，运维完全拿不到失败原因，只能自己猜是哪一段配置坏了。
func TestNewProxyFailureLeavesLogOutputUsable(t *testing.T) {
	capture := detachTestEnv(t)

	const marker = "LOG-DETACH-MARKER-AFTER-NEWPROXY-FAILURE"
	logPath := filepath.Join(t.TempDir(), "agent.log")

	RegisterProxyCreator("LOG-DETACH-TEST", func(context.Context) (*Proxy, error) {
		return nil, common.NewError("creator failed on purpose")
	})
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		delete(creators, "LOG-DETACH-TEST")
	})

	data := []byte(`{"run_type":"LOG-DETACH-TEST","log_level":0,"log_file":"` + logPath + `"}`)
	if p, err := NewProxyFromConfigData(data, true); err == nil {
		_ = p.Close()
		t.Fatal("桩 creator 必须返回错误，否则这条判据没有走到失败路径")
	}

	// 复刻 main 的报错路径：selectAndRun 打 WARN，main 再打 ERROR
	log.Error(common.NewError(marker))

	if got := readCaptured(t, capture); !strings.Contains(got, marker) {
		t.Fatalf("启动失败的错误被吞掉了：输出还指着已关闭的日志文件，stderr 收到的是 %q", got)
	}
	// 日志文件确实被打开过（否则上面的交还毫无意义）
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("log_file 没有被创建：%v", err)
	}
}

// TestCloseLeavesLogOutputUsableForTheFinalReport 守第二个关闭点：Proxy.Close 在
// 函数体内就关掉了日志文件，而它的返回值要等 RunAndClose 合并上抛、main 打印之后
// 进程才退出。同一条「输出指着已关闭文件」的坑在正常退出路径上也会把收尾的
// ERROR/FATAL 全部吃掉。
func TestCloseLeavesLogOutputUsableForTheFinalReport(t *testing.T) {
	capture := detachTestEnv(t)

	const marker = "LOG-DETACH-MARKER-AFTER-CLOSE"
	logPath := filepath.Join(t.TempDir(), "agent.log")
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	log.SetOutput(file)

	p := NewProxy(context.Background(), func() {}, nil, &closeStub{})
	p.logFile = file

	if err := p.Close(); err == nil {
		t.Fatal("桩 sink 的 Close 必须返回错误，才能复刻 main 需要报告的情形")
	}

	log.Error(common.NewError(marker))

	if got := readCaptured(t, capture); !strings.Contains(got, marker) {
		t.Fatalf("Close 之后的日志全丢了：输出没有随日志文件一起交还 stderr，stderr 收到的是 %q", got)
	}
	// 已交还的输出不能留着指针，否则第二次 Close 会把 stderr 再「交还」一次、
	// 也把关闭错误重复报一遍
	if p.logFile != nil {
		t.Fatal("Close 必须把 logFile 置 nil，保证重复调用不会再动全局输出")
	}
}
