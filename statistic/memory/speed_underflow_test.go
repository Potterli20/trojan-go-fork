package memory

import (
	"context"
	"math"
	"testing"
	"testing/synctest"
	"time"
)

// 管理员通过 gRPC SetUsers 调低用户累计用量（正常操作）时，setTraffic 只 Store 新值、
// 不回写 lastSent/lastRecv。下一秒 speedUpdater 执行 sent - lastSent.Swap(sent)，
// 当 sent < lastSent 时 uint64 下溢成约 1.8e19 的伪速率，经 GetSpeed 原样出现在
// api/service/server.go 的 GetUsers 输出里。ResetTraffic 早就显式清零 last* 规避了
// 同一个下溢（见 memory.go），setTraffic 这条并行写入路径是漏网的姊妹。
//
// 用 synctest 假时钟精确推进一个 tick；lastSent 是否被 Swap 更新当作"ticker 确实跑过"
// 的同步点，避免把"压根没跑"误判成"跑了但结果是 0"。
func TestSpeedUpdaterDoesNotUnderflowWhenTrafficDecreases(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		u := &User{ctx: ctx}

		// 模拟已跑出流量基线
		u.Sent.Store(1000)
		u.Recv.Store(1000)
		u.lastSent.Store(1000)
		u.lastRecv.Store(1000)

		// 管理员把累计用量调低（合法值）
		u.setTraffic(100, 100)

		go u.speedUpdater()

		time.Sleep(1500 * time.Millisecond) // 假时钟推进，确保至少一次 tick

		// 同步点：ticker 跑过一轮后 lastSent 被 Swap 成当前 Sent
		if got := u.lastSent.Load(); got != 100 {
			cancel()
			t.Fatalf("speedUpdater 没有执行过 tick（lastSent=%d），判据失效", got)
		}

		sent, recv := u.GetSpeed()
		if sent > math.MaxInt64 || recv > math.MaxInt64 {
			cancel()
			t.Fatalf("调低用量后速率下溢成天文数字：sent=%d recv=%d", sent, recv)
		}
	})
}
