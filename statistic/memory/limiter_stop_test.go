package memory

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/time/rate"
)

// TestSessionCtxWakesPacedWait 盯的是限速排队的**唤醒源**。
//
// waitPaced 原先只听 u.ctx（用户被删 / 后端关停才会取消）。但 relay 拆会话走的是关连接，
// 它碰不到 u.ctx —— 于是一个 1 B/s 的用户发 8KB 就要在 AddSentTraffic 里排到几十秒之外，
// Proxy.Close 的 5s 预算只是放弃等待、叫不醒它：这条协程连同它攥着的中继 buffer 一直留在
// 池外（池被抽空之后每包重新 make），库化/热重载的调用方会一段段攒出来。
//
// 判据跑在 Go 1.27 的 synctest 气泡里，"取消会话之后过了多久才返回"是虚拟时间读数：
// 正确实现必须在取消的那一刻醒来，而不是继续按字节排队。
func TestSessionCtxWakesPacedWait(t *testing.T) {
	const limit = 1000 // B/s，burst = 2000 → 8192 字节需要排 ~6.19s

	synctest.Test(t, func(t *testing.T) {
		userCtx, cancelUser := context.WithCancel(context.Background())
		defer cancelUser()
		u := &User{ctx: userCtx, SendLimiter: rate.NewLimiter(rate.Limit(limit), limit*2)}

		sessionCtx, cancelSession := context.WithCancel(context.Background())
		elapsed := make(chan time.Duration, 1)
		go func() {
			start := time.Now()
			u.AddSentTrafficContext(sessionCtx, 8192)
			elapsed <- time.Since(start)
		}()

		synctest.Wait() // 排队协程已确定阻塞在令牌定时器上
		cancelSession()

		select {
		case d := <-elapsed:
			if d > time.Second {
				t.Fatalf("取消会话 ctx 之后限速等待还花了 %v 才返回："+
					"会话 ctx 没有并进排队，关停叫不醒这条协程", d)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("取消会话之后 AddSentTrafficContext 再也没有返回")
		}

		// 等待被中断不等于流量没发生：账目必须照记，否则额度/统计系统性偏低。
		if sent, _ := u.GetTraffic(); sent != 8192 {
			t.Fatalf("被中断的限速等待丢了账目：sent=%d 期望 8192", sent)
		}
	})
}

// TestUserCtxStillWakesPacedWait 是对照：只取消**用户** ctx 时也必须醒，
// 这是修复前唯一存在的唤醒路径（DelUser / Authenticator.Close）。会话 ctx 传 nil 就是
// 这条老语义，不能因为新增唤醒源而退化。
func TestUserCtxStillWakesPacedWait(t *testing.T) {
	const limit = 1000

	synctest.Test(t, func(t *testing.T) {
		userCtx, cancelUser := context.WithCancel(context.Background())
		u := &User{ctx: userCtx, SendLimiter: rate.NewLimiter(rate.Limit(limit), limit*2)}

		elapsed := make(chan time.Duration, 1)
		go func() {
			start := time.Now()
			u.AddSentTrafficContext(nil, 8192) // nil = 退回 AddSentTraffic 的老行为
			elapsed <- time.Since(start)
		}()

		synctest.Wait()
		cancelUser()

		select {
		case d := <-elapsed:
			if d > time.Second {
				t.Fatalf("取消用户 ctx 之后限速等待花了 %v 才返回：u.ctx 这条唤醒路径被改坏了", d)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("取消用户 ctx 之后没有返回")
		}
		if sent, _ := u.GetTraffic(); sent != 8192 {
			t.Fatalf("账目丢失：sent=%d 期望 8192", sent)
		}
	})
}
