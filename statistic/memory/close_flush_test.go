package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/statistic"
)

// recordingPersistencer 只记录写库调用，用来验证关停时的最终 flush。
type recordingPersistencer struct {
	mu       sync.Mutex
	updates  []statisticTraffic
	closeCnt int
}

type statisticTraffic struct {
	hash string
	sent uint64
	recv uint64
}

func (p *recordingPersistencer) Close() error {
	p.mu.Lock()
	p.closeCnt++
	p.mu.Unlock()
	return nil
}

func (p *recordingPersistencer) SaveUser(_ statistic.Metadata) error { return nil }
func (p *recordingPersistencer) DeleteUser(_ string) error           { return nil }
func (p *recordingPersistencer) ListUser(_ func(string, statistic.Metadata) bool) error {
	return nil
}
func (p *recordingPersistencer) LoadUser(_ string) (statistic.Metadata, error) {
	return nil, nil
}

func (p *recordingPersistencer) UpdateUserTraffic(hash string, sent, recv uint64) error {
	p.mu.Lock()
	p.updates = append(p.updates, statisticTraffic{hash: hash, sent: sent, recv: recv})
	p.mu.Unlock()
	return nil
}

// TestCloseFlushesPendingTraffic 盯的是关停时最后一段流量统计被丢弃：
// batchTrafficUpdater 每 10s 写一次库，而 User.Close 的第一件事是 ResetTraffic
// 把累计量清零。不 flush 就会稳定丢掉最后一个周期内已发生的流量——sqlite 持久化
// 下这是真实的数据丢失，进程重启后那部分数字永远回不来。
func TestCloseFlushesPendingTraffic(t *testing.T) {
	pst := &recordingPersistencer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a := &Authenticator{pst: pst, ctx: ctx, cancel: cancel}

	u := &User{
		Hash:    "aabbccddeeff",
		ipTable: map[string]time.Time{},
		ctx:     ctx,
		cancel:  func() {},
	}
	u.Sent.Store(1234)
	u.Recv.Store(567)
	a.users.Store(u.Hash, u)

	a.Close() //gosec:disable -- 错误忽略：测试清理

	pst.mu.Lock()
	defer pst.mu.Unlock()
	if len(pst.updates) == 0 {
		t.Fatal("Close 没有把最后一个周期的流量写库：这部分统计被 ResetTraffic 清零后永久丢失")
	}
	got := pst.updates[0]
	if got.hash != u.Hash || got.sent != 1234 || got.recv != 567 {
		t.Fatalf("flush 写入的内容不对：%+v，期望 hash=%s sent=1234 recv=567", got, u.Hash)
	}
	if s, r := u.GetTraffic(); s != 0 || r != 0 {
		t.Fatalf("Close 之后用户计数应被清零，实际 sent=%d recv=%d", s, r)
	}
	if pst.closeCnt != 1 {
		t.Fatalf("持久化后端应被关闭一次，实际 %d 次", pst.closeCnt)
	}
}

// TestCloseSkipsFlushWhenNothingChanged 是同一路径的对照：没有新流量就不该多写一次库，
// 否则上一条测试的判据"有写入"就无法区分"确实 flush"和"无脑全量写库"。
func TestCloseSkipsFlushWhenNothingChanged(t *testing.T) {
	pst := &recordingPersistencer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a := &Authenticator{pst: pst, ctx: ctx, cancel: cancel}
	u := &User{Hash: "same", ipTable: map[string]time.Time{}, ctx: ctx, cancel: func() {}}
	u.Sent.Store(10)
	u.Recv.Store(20)
	u.persistedSent.Store(10) // 已经持久化过
	u.persistedRecv.Store(20)
	a.users.Store(u.Hash, u)

	a.Close() //gosec:disable -- 错误忽略：测试清理

	pst.mu.Lock()
	defer pst.mu.Unlock()
	if len(pst.updates) != 0 {
		t.Fatalf("无变化却写库 %d 次：%+v", len(pst.updates), pst.updates)
	}
}
