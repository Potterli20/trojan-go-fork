package recorder

import (
	"testing"
)

// withRestoredCapacity 把包级容量存回去：这几个用例会改全局状态，
// 不还原就会污染同包后面的测试（而且污染方向取决于跑的顺序）。
func withRestoredCapacity(t *testing.T) {
	t.Helper()
	orig := subscriberCapacity.Load()
	t.Cleanup(func() { subscriberCapacity.Store(orig) })
}

// TestOversizedCapacityIsClampedAtSubscribe 是主判据：钳制必须真的落到
// Subscribe 的那次分配上，而不只是改改变量。1<<20 条 = 136MB（Record 实测 136 字节），
// 而容量是订阅那一刻一次性预分配的 —— 溢出兜底不做，一个 API 客户端调一次
// GetRecords 就能把进程打到 runtime out of memory（throw，recover 抓不住）。
func TestOversizedCapacityIsClampedAtSubscribe(t *testing.T) {
	withRestoredCapacity(t)
	for _, n := range []int{1 << 20, 2_000_000_000, 1 << 40} {
		SetCapacity(n)
		if got := subscriberCapacity.Load(); got != maxSubscriberCapacity {
			t.Fatalf("SetCapacity(%d) 后全局容量 = %d，期望钳制到 %d", n, got, maxSubscriberCapacity)
		}
		ch, err := Subscribe("clamp-check", "", "", false)
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		if cap(ch) != maxSubscriberCapacity {
			t.Fatalf("Subscribe 实际分配了 cap=%d，期望 %d", cap(ch), maxSubscriberCapacity)
		}
		Unsubscribe("clamp-check")
	}
}

// TestSaneCapacityIsPassedThrough 是反向护栏：合法值不能被"顺手收紧"改掉，
// 边界值本身（正好等于上限）也不该被降级。
func TestSaneCapacityIsPassedThrough(t *testing.T) {
	withRestoredCapacity(t)
	for _, n := range []int{1, 10, 1000, maxSubscriberCapacity} {
		SetCapacity(n)
		if got := subscriberCapacity.Load(); got != int32(n) {
			t.Fatalf("SetCapacity(%d) 之后 = %d，应当原样生效", n, got)
		}
	}
}

// TestNonPositiveCapacityKeepsPreviousValue 钉住既有语义：
// 配置里没写 record_capacity（零值）时不得把缓冲压成 0/负数——
// 那会让每个订阅者的 channel 容量变成不可预期的值。
func TestNonPositiveCapacityKeepsPreviousValue(t *testing.T) {
	withRestoredCapacity(t)
	SetCapacity(64)
	for _, n := range []int{0, -1, -1 << 30} {
		SetCapacity(n)
		if got := subscriberCapacity.Load(); got != 64 {
			t.Fatalf("SetCapacity(%d) 不该改动既有值，实际 = %d", n, got)
		}
	}
}
