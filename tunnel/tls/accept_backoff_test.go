package tls

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// panicAcceptUnderlay 模拟下层在解析**未认证对端字节**时反复 panic：
// AcceptConn 前 panics 次直接 panic（被 AcceptConnGuarded 转成 ErrAcceptPanic），
// 之后返回一个普通错误，并在到达 limit 时触发 onLimit（测试里是 cancel）。
//
// 为什么达到上限后改吐普通错误而不是继续 panic：TLS 修复前的 panic 分支是裸
// continue、连 s.ctx 都不看，若一直 panic，退避缺失时循环永远自转、synctest
// 气泡里的虚拟时间永不推进，判据只会挂到 test timed out 而不是报出确切次数。
// 吐一个普通错误后，旧实现会从"非 panic 分支"的 ctx 检查退出，于是判据能
// 明确地报 "调用 N 次 > 阈值" 这条原因。
type panicAcceptUnderlay struct {
	calls   atomic.Int64
	panics  int64
	limit   int64
	limitMu sync.Mutex
	onLimit func()
}

func (u *panicAcceptUnderlay) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	n := u.calls.Add(1)
	if n <= u.panics {
		panic("corrupt peer bytes during handshake")
	}
	if n >= u.limit {
		u.limitMu.Lock()
		fn := u.onLimit
		u.onLimit = nil
		u.limitMu.Unlock()
		if fn != nil {
			fn()
		}
	}
	return nil, errors.New("accept unavailable")
}

func (u *panicAcceptUnderlay) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, errors.New("not used")
}

func (u *panicAcceptUnderlay) Close() error { return nil }

// TestPanicAcceptLoopBacksOff 盯住 acceptLoop 的 panic 分支：下层若持续在
// 未认证字节上 panic（畸形握手），旧实现是裸 log.Error + continue，既无退避
// 也无日志限流，更不看 ctx —— 单条 accept 协程满 CPU 自转，且每行一条 Error。
// 同型的 trojan（`tunnel/trojan/server.go`）、simplesocks（`tunnel/simplesocks/server.go`）
// 早已把 panic 纳入"线性退避 + 每 10 次一条 Error + ctx 唤醒"，TLS 是唯一漏网。
//
// 判据跑在 Go 1.27 的 testing/synctest 气泡里：虚拟时间只在所有协程 durably
// blocked 时才推进，所以"500ms 里 Accept 被调用几次"是精确读数，不吃机器负载。
// 正向对照：把 panic 分支改回裸 continue，调用次数会冲到 limit(201) 才停，
// 判据当场变红。
func TestPanicAcceptLoopBacksOff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	underlay := &panicAcceptUnderlay{panics: 200, limit: 201}
	s := &Server{
		ctx:      ctx,
		cancel:   cancel,
		underlay: underlay,
	}
	underlay.onLimit = cancel

	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		go func() {
			s.acceptLoop()
			close(done)
		}()

		time.Sleep(500 * time.Millisecond)
		calls := underlay.calls.Load()
		if calls == 0 {
			t.Fatal("用例没进入 panic 分支，判据失效")
		}
		// 线性退避 100/200/300ms（封顶 3s）：500ms 虚拟时间内至多约 3 次进入
		// AcceptConn。阈值 6 足以把"裸 continue 自转"（会冲到 201 次）判死。
		if calls > 6 {
			t.Fatalf("500ms 虚拟时间里 AcceptConn 被调用了 %d 次：panic 分支没有退避，满 CPU 自转", calls)
		}

		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("ctx 取消后 acceptLoop 没有退出：panic 退避必须同时听 ctx.Done")
		}
	})
}
