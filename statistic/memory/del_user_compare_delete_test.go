package memory

import (
	"context"
	"testing"

	"github.com/Potterli20/trojan-go-fork/config"
)

// TestDelUserDoesNotDeleteReplacedUser 钉住 DelUser 的 compare-and-delete 语义。
//
// 旧实现是 Load(hash) → meter.Close()（含 wg.Wait，窗口不小）→ Delete(hash)：
// Delete 无条件按键删，而不是"只有表里仍是我 Close 掉的那个对象时才删"。
// 真实并发序列（API SetUsers 的 Delete/Add 与 mysql syncUsers 可对同一 hash 撞上）：
//
//	A: Load(H)→u1，停在 Close 之后
//	B: 完整 DelUser(H) → Close(u1)、删掉 u1
//	C: AddUser(H) → 建 u2、起 speedUpdater/ipCleaner
//	A: Delete(H) → 把 u2 删掉
//
// 后果：u2 仍被 relay/API 当作活用户持有，却已从 users 表摘除；它的后台协程、
// ctx、ipTable 无人 Close，永久泄漏。这与 AddUser 用 LoadOrStore 是两回事——
// 那处防覆盖，这里防"删除的键与所删对象不一致"。
//
// 判据用包级注入钩子 delUserAfterClose 把 A 停在 Close 之后，B/C 由测试直接驱动，
// 全程通道同步、不靠墙钟。变异对照：把 CompareAndDelete 换回无条件 Delete，
// 本用例立刻转红（u2 被 A 删掉）。
func TestDelUserDoesNotDeleteReplacedUser(t *testing.T) {
	iauth, err := NewAuthenticator(config.WithConfig(context.Background(), Name, &Config{}))
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	auth, ok := iauth.(*Authenticator)
	if !ok {
		t.Fatalf("NewAuthenticator 返回类型变了: %T", iauth)
	}
	defer auth.Close() //gosec:disable -- 错误忽略：测试清理

	const hash = "del-user-compare-delete"
	if err := auth.AddUser(hash); err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	u1, ok := auth.users.Load(hash)
	if !ok {
		t.Fatal("u1 未登记")
	}

	// A 停在 Close 之后、删除之前。
	paused := make(chan struct{})
	resume := make(chan struct{})
	restore := delUserAfterClose
	delUserAfterClose = func() {
		close(paused)
		<-resume
	}
	t.Cleanup(func() { delUserAfterClose = restore })

	aDone := make(chan error, 1)
	go func() { aDone <- auth.DelUser(hash) }()
	<-paused // A 已 Close 掉 u1，停在删除前

	// B：这时表项仍是 u1（A 还没删），直接模拟另一次完整删除。
	// 直接操作 users 表，避免再触发钩子。
	if cur, found := auth.users.Load(hash); !found || cur != u1 {
		t.Fatal("B 看到的表项不是 u1，夹具失效")
	}
	u1.(*User).Close() //gosec:disable -- 错误忽略：清理
	auth.users.Delete(hash)

	// C：同 hash 重建一个新用户。
	if err := auth.AddUser(hash); err != nil {
		t.Fatalf("C AddUser: %v", err)
	}
	u2, ok := auth.users.Load(hash)
	if !ok || u2 == u1 {
		t.Fatal("C 没有建出新的 u2")
	}

	// 放行 A：它的删除只能作用于当初那个 u1，不能碰 u2。
	close(resume)
	if err := <-aDone; err != nil {
		t.Fatalf("A DelUser: %v", err)
	}

	if cur, found := auth.users.Load(hash); !found || cur != u2 {
		t.Fatal("A 的延迟删除把刚重建的 u2 删掉了：DelUser 缺少 compare-and-delete 语义")
	}
	// u2 必须还活着（cancel 没被误触发）：它还要服务后续连接。
	select {
	case <-u2.(*User).ctx.Done():
		t.Fatal("u2 的 ctx 被误取消")
	default:
	}
}
