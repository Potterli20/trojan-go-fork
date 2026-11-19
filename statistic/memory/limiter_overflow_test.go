package memory

import (
	"context"
	"math"
	"testing"
)

// SetSpeedLimit 的 burst 取 2*limit。limit > MaxInt/2 时这个乘法按有符号 int 回绕成
// 负数，x/time/rate 的 NewLimiter 又不校验 burst（v0.16 直接赋值），于是 waitPaced
// 里 take=min(n,burst) 为负、WaitN(负) 恒 ok 且立即返回，n 每次 +2 —— 会话协程
// 100% CPU 空转、永不推进。一条 gRPC SetUsers 把 speed_limit 设为 MaxInt 就能触发。
// 判据钉住源头事实：任何合法 limit 都不得造出非正的 burst。
func TestSetSpeedLimitBurstNeverOverflows(t *testing.T) {
	for _, limit := range []int{math.MaxInt, math.MaxInt/2 + 1, 1 << 62} {
		u := &User{ctx: context.Background()}
		u.SetSpeedLimit(limit, limit)
		if got := u.SendLimiter.Burst(); got <= 0 {
			t.Errorf("send limit=%d 造出 burst=%d：waitPaced 会因负 take 死循环", limit, got)
		}
		if got := u.RecvLimiter.Burst(); got <= 0 {
			t.Errorf("recv limit=%d 造出 burst=%d：下行同一缺口", limit, got)
		}
	}
}
