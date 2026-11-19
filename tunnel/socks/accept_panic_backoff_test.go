package socks

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

// panicAcceptUnderlay 模拟下层在解析未认证字节时反复 panic，达到 limit 后吐普通
// 错误并触发 onLimit（cancel）。旧实现从 limit 之后那次普通错误的 ctx 检查退出，
// 于是判据能明确报出"调用次数 > 阈值"；若 panic 分支继续裸 continue，气泡里的
// 虚拟时间不会推进（无退避），循环会一直转到 limit 才停。
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

// socks 的 acceptConnLoop 是最后一个 panic 分支仍裸 continue 的入站：tls/trojan/
// simplesocks/http/mux 都把 ErrAcceptPanic 纳入"线性退避 + 每 10 次一条 Error +
// ctx 唤醒"，唯独 socks 的 panic 分支在 ctx 检查之前 continue。对端持续发畸形
// 握手字节即可让单条 accept 协程满 CPU 自转、日志刷屏，且 Close 的 wg.Wait 挂死。
func TestPanicAcceptConnLoopBacksOff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	underlay := &panicAcceptUnderlay{panics: 200, limit: 201}
	s := &Server{ctx: ctx, cancel: cancel, underlay: underlay}
	underlay.onLimit = cancel

	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		go func() {
			s.acceptConnLoop()
			close(done)
		}()

		time.Sleep(500 * time.Millisecond)
		calls := underlay.calls.Load()
		if calls == 0 {
			t.Fatal("用例没进入 panic 分支，判据失效")
		}
		// 线性退避 100/200/300ms：500ms 虚拟时间内至多约 3 次进入 AcceptConn。
		// 阈值 6 足以把"裸 continue 自转"（冲到 201 次）判死。
		if calls > 6 {
			t.Fatalf("500ms 虚拟时间里 AcceptConn 被调用了 %d 次：panic 分支没有退避，满 CPU 自转", calls)
		}

		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("ctx 取消后 acceptConnLoop 没有退出：panic 退避必须同时听 ctx.Done")
		}
	})
}
