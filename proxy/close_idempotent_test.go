package proxy

import (
	"context"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// countingClient / countingServer 记录 Close 调用次数，用来钉住"并发 Close 只生效一次"。
type countingClient struct{ closes atomic.Int64 }

func (c *countingClient) DialConn(*tunnel.Address, tunnel.Tunnel) (tunnel.Conn, error) {
	return nil, io.EOF
}
func (c *countingClient) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, io.EOF
}
func (c *countingClient) Close() error { c.closes.Add(1); return nil }

type countingServer struct{ closes atomic.Int64 }

func (s *countingServer) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) { return nil, io.EOF }
func (s *countingServer) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, io.EOF
}
func (s *countingServer) Close() error { s.closes.Add(1); return nil }

// TestConcurrentCloseIsIdempotent 钉住导出 Close 在并发/重复调用下的三件不变量：
//  1. 无 data race —— 修复前 p.logFile 的无锁读改写（Close 里 detachLogFile(p.logFile)
//     读、p.logFile = nil 写）在 -race 下必报；库化调用方反复建栈/热重载会并发关停。
//  2. 副作用只发生一次 —— sink/source 的 Close 各一次，日志文件只关一次。
//  3. 返回值一致 —— 所有调用者拿到同一份结果。
//
// 变异对照：去掉 closeOnce 幂等包装，本用例在 -race 下立刻报 data race，
// 且 countingClient.closes 会变成 N。
func TestConcurrentCloseIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sink := &countingClient{}
	source := &countingServer{}
	p := NewProxy(ctx, cancel, []tunnel.Server{source}, sink)

	f, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	p.logFile = f

	const n = 8
	var start sync.WaitGroup
	start.Add(1)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start.Wait()
			errs[i] = p.Close() //gosec:disable -- 错误忽略：一律 nil，下面逐条断言
		}(i)
	}
	start.Done()
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("第 %d 次 Close 返回错误：%v", i, err)
		}
	}
	if got := sink.closes.Load(); got != 1 {
		t.Fatalf("sink.Close 被调用 %d 次，幂等 Close 应为 1 次", got)
	}
	if got := source.closes.Load(); got != 1 {
		t.Fatalf("source.Close 被调用 %d 次，幂等 Close 应为 1 次", got)
	}
	// 文件若被关两次，第二次会报 "file already closed" 并写进 errs；上面已断言 errs 全 nil。
	// 这里再确认日志输出已交还 stderr（不被关掉的文件吞掉）。
	if p.logFile != nil {
		t.Fatal("Close 之后 logFile 应被置 nil")
	}
}
