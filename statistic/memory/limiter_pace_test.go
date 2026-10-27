package memory

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/time/rate"
)

// 限速必须真的按字节排队。旧写法是一次 limiter.WaitN(ctx, n)：当 n 大于 burst
// （burst 取 2*limit）时 x/time/rate **立即返回错误而不是等待**，而中继每块最大能到
// relay_buffer_size（默认 8192，上限 1MB）。于是 speed_limit 低于 4096 B/s 的用户
// 完全不设限，错误又被调用点丢掉——运营方以为限额生效，实际一条都没限。
//
// 用 synctest 的假时钟量这段等待：气泡内时间只在所有 goroutine 都 durably blocked 时
// 才前进，所以读数精确、不吃真实墙钟，也不存在"睡够就算过"的漂移。
func TestAddSentTrafficPacesBeyondBurst(t *testing.T) {
	const limit = 1000 // B/s，burst = 2000
	synctest.Test(t, func(t *testing.T) {
		u := &User{ctx: context.Background(), SendLimiter: rate.NewLimiter(rate.Limit(limit), limit*2)}
		start := time.Now()
		u.AddSentTraffic(8192)
		elapsed := time.Since(start)

		// 需要额外排队的量 = 8192 - burst(2000)，按 1000 B/s 至少 6.192s。
		if elapsed < 6*time.Second {
			t.Fatalf("8192 字节只花了 %v 就返回（burst=%d, limit=%d B/s）："+
				"超过 burst 的请求必须拆成多次等待，否则限额对任何一块都形同虚设",
				elapsed, u.SendLimiter.Burst(), limit)
		}
		if elapsed > 8*time.Second {
			t.Fatalf("等了 %v，超过按速率应有的 6.2s 上限：拆分逻辑多半在重复等待", elapsed)
		}
		if sent, recv := u.GetTraffic(); sent != 8192 || recv != 0 {
			t.Fatalf("排队之后流量账目必须照记，实际 sent=%d recv=%d", sent, recv)
		}
	})
}

// 下行是姊妹分支，口径必须一致——两处共用一个 waitPaced 才算修完。
func TestAddRecvTrafficPacesBeyondBurst(t *testing.T) {
	const limit = 1000
	synctest.Test(t, func(t *testing.T) {
		u := &User{ctx: context.Background(), RecvLimiter: rate.NewLimiter(rate.Limit(limit), limit*2)}
		start := time.Now()
		u.AddRecvTraffic(8192)
		elapsed := time.Since(start)
		if elapsed < 6*time.Second {
			t.Fatalf("下行 8192 字节只花了 %v：与上行不同口径", elapsed)
		}
	})
}

// 在 burst 之内时不该多等：这条防止"拆分"被写成每次都强制等一个周期。
func TestAddSentTrafficWithinBurstDoesNotWait(t *testing.T) {
	const limit = 1000
	synctest.Test(t, func(t *testing.T) {
		u := &User{ctx: context.Background(), SendLimiter: rate.NewLimiter(rate.Limit(limit), limit*2)}
		start := time.Now()
		u.AddSentTraffic(1500) // 小于 burst，令牌桶初始就是满的
		// 气泡内没人阻塞就绝不推进假时钟，所以"等了多久"是一个精确读数而不是阈值猜测。
		if elapsed := time.Since(start); elapsed != 0 {
			t.Fatalf("burst 之内的请求不该等待，实际推进了假时钟 %v", elapsed)
		}
	})
}
