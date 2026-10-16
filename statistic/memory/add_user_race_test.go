package memory

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
)

// TestConcurrentAddUserSameHashDoesNotOrphanUpdaters 盯 AddUser 的 check-then-act。
//
// 旧写法是 `users.Load(hash)` 判存在、然后建 User 起两个后台协程、最后 `users.Store`。
// 中间那个窗口里，两个并发的 AddUser(同一 hash) 都能通过存在性检查：各自起
// speedUpdater + ipCleaner，然后后写的把先写的覆盖掉。被覆盖的那个 User 再没有任何
// 引用者——DelUser 只关 map 里那一个——于是它的 cancel 永远不会被调用，
// 每撞一次永久漏 2 个 goroutine + 2 个 ticker + 1 个 ctx + 1 张 ipTable。
// 触发面真实存在：API 的 SetUsers(Add) 与 mysql 后端的 syncUsers 都会对同一 hash 调它。
//
// 这是个竞态，判据只能按概率做：多轮成对并发，任何一轮漏下的协程都不会自己消失，
// 所以最后的总数必须等于"基线 + 成功用户数 × 2"。跑够多轮之后漏一个也一定被数出来。
func TestConcurrentAddUserSameHashDoesNotOrphanUpdaters(t *testing.T) {
	auth, err := NewAuthenticator(config.WithConfig(context.Background(), Name, &Config{}))
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	defer auth.Close() //gosec:disable -- 错误忽略：测试清理

	const (
		rounds        = 120
		goroutinesPer = 2 // speedUpdater + ipCleaner
	)

	// 基线：认证器自己的后台协程（清扫循环等）
	settleGoroutines(t, runtime.NumGoroutine())
	base := runtime.NumGoroutine()

	var added sync.Map // hash -> 成功登记过
	overlapped := 0
	for r := range rounds {
		hash := common.SHA224String("race-" + string(rune('a'+r%26)) + string(rune('a'+r/26)))
		var wg sync.WaitGroup
		var okCount atomicInt
		for range 2 {
			wg.Go(func() {
				if err := auth.AddUser(hash); err == nil {
					okCount.add(1)
					added.Store(hash, struct{}{})
				}
			})
		}
		wg.Wait()
		if n := okCount.get(); n != 1 {
			// 不在这里 Fatal：让后面的协程计数一并跑出来，回归时能同时看到
			// "重复登记"和"孤儿后台协程"两个后果
			overlapped++
		}
	}

	users := 0
	added.Range(func(_, _ any) bool { users++; return true })

	// 每个成功用户留 2 个常驻协程；抢输的那些必须已经把自己收干净
	target := base + users*goroutinesPer
	got := settleGoroutines(t, target)
	if overlapped > 0 {
		// Errorf 不 Fatal：让下面的协程上限量也报出来，一次看全两个后果
		t.Errorf("%d 轮成对并发里有 %d 轮出现了同 hash 重复登记（check-then-act 的窗口）",
			rounds, overlapped)
	}
	if got > target {
		t.Fatalf("%d 轮成对并发之后协程数 %d，上限应为 %d（基线 %d + %d 个用户 × %d）："+
			"多出来的是被覆盖那个 User 留下的孤儿 speedUpdater/ipCleaner——它的 cancel 没人再持有",
			rounds, got, target, base, users, goroutinesPer)
	}
}

// settleGoroutines 轮询直到协程数降到 target 以下或超时，返回最后一次观测值。
func settleGoroutines(t *testing.T, target int) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	cur := runtime.NumGoroutine()
	for cur > target && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		cur = runtime.NumGoroutine()
	}
	return cur
}

// atomicInt 避免为一个测试再引 sync/atomic 到测试文件顶部。
type atomicInt struct {
	mu  sync.Mutex
	val int
}

func (a *atomicInt) add(n int) {
	a.mu.Lock()
	a.val += n
	a.mu.Unlock()
}

func (a *atomicInt) get() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.val
}
