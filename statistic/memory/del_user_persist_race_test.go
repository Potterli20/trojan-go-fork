package memory

import (
	"context"
	"sync"
	"testing"

	"github.com/Potterli20/trojan-go-fork/statistic"
)

// R17-1：DelUser 输掉 CompareAndDelete 之后仍无条件调 pst.DeleteUser，
// 会把并发"删完又重建"的同 hash 新用户的持久化行抹掉。
//
// 后果：内存里有活用户 u2、DB 中却无行，下次重启该用户直接消失；
// 若后端是 sqlite，这条 Delete 还连带删掉面板配置的限速/到期字段。
//
// 判据用 delUserAfterClose 钩子把 A 停在 Close 之后、CompareAndDelete 之前，
// B/C 由测试直接驱动（形态同 del_user_compare_delete_test.go），
// 并以一个记录删除 hash 的假 Persistencer 判断持久化行是否被动过。
// 变异对照：把生产代码里 `if deleted { pst.DeleteUser }` 改回无条件调用，
// 本用例立刻转红。
type deleteRecordingPersistencer struct {
	mu      sync.Mutex
	deleted []string
}

func (p *deleteRecordingPersistencer) SaveUser(statistic.Metadata) error           { return nil }
func (p *deleteRecordingPersistencer) LoadUser(string) (statistic.Metadata, error) { return nil, nil }
func (p *deleteRecordingPersistencer) ListUser(func(string, statistic.Metadata) bool) error {
	return nil
}
func (p *deleteRecordingPersistencer) UpdateUserTraffic(string, uint64, uint64) error {
	return nil
}
func (p *deleteRecordingPersistencer) Close() error { return nil }
func (p *deleteRecordingPersistencer) DeleteUser(hash string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deleted = append(p.deleted, hash)
	return nil
}
func (p *deleteRecordingPersistencer) calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.deleted...)
}

func TestDelUserLostRaceDoesNotDeletePersistedRow(t *testing.T) {
	pst := &deleteRecordingPersistencer{}
	ctx, cancel := context.WithCancel(context.Background())
	auth := &Authenticator{ctx: ctx, cancel: cancel, pst: pst}
	defer auth.Close() //gosec:disable -- 错误忽略：测试清理

	const hash = "del-user-persist-race"
	if err := auth.AddUser(hash); err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	u1, ok := auth.users.Load(hash)
	if !ok {
		t.Fatal("u1 未登记")
	}

	// A 停在 Close 之后、CompareAndDelete 之前。
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
	<-paused

	// B：另一次完整删除（直接操作表，避免触发钩子）。
	u1.(*User).Close() //gosec:disable -- 错误忽略：清理
	auth.users.Delete(hash)
	// C：同 hash 重建新用户，其 DB 行由 SetKeyShare 等写入。
	if err := auth.AddUser(hash); err != nil {
		t.Fatalf("C AddUser: %v", err)
	}
	u2, ok := auth.users.Load(hash)
	if !ok || u2 == u1 {
		t.Fatal("C 没有建出新的 u2")
	}

	// 放行 A：它输掉了比较，不能碰 u2 的持久化行。
	close(resume)
	if err := <-aDone; err != nil {
		t.Fatalf("A DelUser: %v", err)
	}
	if cur, found := auth.users.Load(hash); !found || cur != u2 {
		t.Fatal("夹具失效：A 把 u2 从内存删掉了")
	}
	if calls := pst.calls(); len(calls) != 0 {
		t.Fatalf("输掉 CompareAndDelete 的 DelUser 仍删了持久化行: %v（u2 的 DB 行被抹掉，重启将丢用户）", calls)
	}
}
