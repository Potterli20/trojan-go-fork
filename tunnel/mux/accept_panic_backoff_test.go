package mux

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

// 与 http 同型：d2b18312 只修了通用错误分支，mux acceptConnWorker 的 panic
// 分支仍在 ctx 检查之前裸 continue。下层持续 panic 时满 CPU + 日志刷屏，
// Close 取消 ctx 后 wg.Wait 永久挂死。
func TestPanicAcceptWorkerBacksOff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	underlay := &panicAcceptUnderlay{panics: 200, limit: 201}
	s := &Server{ctx: ctx, cancel: cancel, underlay: underlay}
	underlay.onLimit = cancel

	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		go func() {
			s.acceptConnWorker()
			close(done)
		}()

		time.Sleep(500 * time.Millisecond)
		calls := underlay.calls.Load()
		if calls == 0 {
			t.Fatal("用例没进入 panic 分支，判据失效")
		}
		if calls > 6 {
			t.Fatalf("500ms 虚拟时间里 AcceptConn 被调用了 %d 次：panic 分支没有退避，满 CPU 自转", calls)
		}

		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("ctx 取消后 acceptConnWorker 没有退出：panic 退避必须同时听 ctx.Done")
		}
	})
}
