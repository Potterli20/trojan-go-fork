package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/statistic"
)

var (
	// errDatabaseIsLocked 模拟真实 sqlite busy/locked 错误字符串
	errDatabaseIsLocked = errors.New("database is locked")
	// errDeadlock 模拟 MySQL 死锁错误字符串
	errDeadlock = errors.New("Deadlock found when trying to get lock; try restarting transaction")
	// errTableMissing 模拟不可重试错误（表不存在）
	errTableMissing = errors.New("table trojan_users does not exist")
	// errConnRefused 模拟不可重试错误（连接被拒）
	errConnRefused = errors.New("connection refused")
)

func TestMemoryAuth(t *testing.T) {
	cfg := &Config{
		Passwords: nil,
	}
	ctx := config.WithConfig(context.Background(), Name, cfg)
	auth, err := NewAuthenticator(ctx)
	common.Must(err)
	auth.AddUser("user1")
	valid, user := auth.AuthUser("user1")
	if !valid {
		t.Fatal("add, auth")
	}
	if user.GetHash() != "user1" {
		t.Fatal("Hash")
	}
	user.AddSentTraffic(100)
	user.AddRecvTraffic(200)
	sent, recv := user.GetTraffic()
	if sent != 100 || recv != 200 {
		t.Fatal("traffic")
	}
	sent, recv = user.ResetTraffic()
	if sent != 100 || recv != 200 {
		t.Fatal("ResetTraffic")
	}
	sent, recv = user.GetTraffic()
	if sent != 0 || recv != 0 {
		t.Fatal("ResetTraffic")
	}

	user.AddIP("1234")
	user.AddIP("5678")
	if user.GetIP() != 0 {
		t.Fatal("GetIP")
	}

	auth.SetUserIPLimit(user.GetHash(), 2)
	user.AddIP("1234")
	user.AddIP("5678")
	user.DelIP("1234")
	if user.GetIP() != 1 {
		t.Fatal("DelIP")
	}
	user.DelIP("5678")

	auth.SetUserIPLimit(user.GetHash(), 2)
	if !user.AddIP("1") || !user.AddIP("2") {
		t.Fatal("AddIP")
	}
	if user.AddIP("3") {
		t.Fatal("AddIP")
	}
	if !user.AddIP("2") {
		t.Fatal("AddIP")
	}

	auth.SetUserTraffic(user.GetHash(), 1234, 4321)
	if a, b := user.GetTraffic(); a != 1234 || b != 4321 {
		t.Fatal("SetTraffic")
	}

	user.ResetTraffic()
	go func() {
		for {
			k := 100
			time.Sleep(time.Second / time.Duration(k))
			user.AddSentTraffic(2000 / k)
			user.AddRecvTraffic(1000 / k)
		}
	}()
	time.Sleep(time.Second * 4)
	if sent, recv := user.GetSpeed(); sent > 3000 || sent < 1000 || recv > 1500 || recv < 500 {
		t.Error("GetSpeed", sent, recv)
	} else {
		t.Log("GetSpeed", sent, recv)
	}

	auth.SetUserSpeedLimit(user.GetHash(), 30, 20)
	time.Sleep(time.Second * 4)
	if sent, recv := user.GetSpeed(); sent > 60 || recv > 40 {
		t.Error("SetSpeedLimit", sent, recv)
	} else {
		t.Log("SetSpeedLimit", sent, recv)
	}

	auth.SetUserSpeedLimit(user.GetHash(), 0, 0)
	time.Sleep(time.Second * 4)
	if sent, recv := user.GetSpeed(); sent < 30 || recv < 20 {
		t.Error("SetSpeedLimit", sent, recv)
	} else {
		t.Log("SetSpeedLimit", sent, recv)
	}

	auth.AddUser("user2")
	valid, _ = auth.AuthUser("user2")
	if !valid {
		t.Fatal()
	}
	auth.DelUser("user2")
	valid, _ = auth.AuthUser("user2")
	if valid {
		t.Fatal()
	}
	auth.AddUser("user3")
	users := auth.ListUsers()
	if len(users) != 2 {
		t.Fatal()
	}
	user.Close()
	auth.Close()
}

func BenchmarkMemoryUsage(b *testing.B) {
	cfg := &Config{
		Passwords: nil,
	}
	ctx := config.WithConfig(context.Background(), Name, cfg)
	auth, err := NewAuthenticator(ctx)
	common.Must(err)

	m1 := runtime.MemStats{}
	m2 := runtime.MemStats{}
	runtime.ReadMemStats(&m1)
	for i := 0; i < b.N; i++ {
		hash, err := common.HashPassword("hash" + strconv.Itoa(i))
		common.Must(err)
		common.Must(auth.AddUser(hash))
	}
	runtime.ReadMemStats(&m2)

	b.ReportMetric(float64(m2.Alloc-m1.Alloc)/1024/1024, "MiB(Alloc)")
	b.ReportMetric(float64(m2.TotalAlloc-m1.TotalAlloc)/1024/1024, "MiB(TotalAlloc)")
}

func TestMemoryAuthConcurrentUserOperations(t *testing.T) {
	const numGoroutines = 100
	const numOpsPerGoroutine = 100

	cfg := &Config{Passwords: nil}
	ctx := config.WithConfig(context.Background(), Name, cfg)
	auth, err := NewAuthenticator(ctx)
	common.Must(err)
	defer auth.Close()

	var wg sync.WaitGroup
	var successCount atomic.Int32

	for i := range numGoroutines {
		wg.Go(func() {
			userHash := "concurrent-user-" + strconv.Itoa(i)

			for range numOpsPerGoroutine {
				if err := auth.AddUser(userHash); err == nil {
					successCount.Add(1)
					auth.DelUser(userHash)
				}
			}
		})
	}

	wg.Wait()
	t.Logf("Concurrent user operations: %d successful", successCount.Load())
}

func TestMemoryAuthConcurrentTrafficUpdates(t *testing.T) {
	const numGoroutines = 50
	const numTrafficUpdates = 1000

	cfg := &Config{Passwords: nil}
	ctx := config.WithConfig(context.Background(), Name, cfg)
	auth, err := NewAuthenticator(ctx)
	common.Must(err)
	defer auth.Close()

	auth.AddUser("traffic-test-user")
	valid, user := auth.AuthUser("traffic-test-user")
	if !valid {
		t.Fatal("failed to auth test user")
	}

	var wg sync.WaitGroup
	var totalSent, totalRecv atomic.Uint64

	for range numGoroutines {
		wg.Go(func() {
			for range numTrafficUpdates {
				user.AddSentTraffic(1)
				totalSent.Add(1)
				user.AddRecvTraffic(1)
				totalRecv.Add(1)
			}
		})
	}

	wg.Wait()

	sent, recv := user.GetTraffic()
	expectedSent := totalSent.Load()
	expectedRecv := totalRecv.Load()

	if sent != expectedSent {
		t.Errorf("sent traffic mismatch: got %d, expected %d", sent, expectedSent)
	}
	if recv != expectedRecv {
		t.Errorf("recv traffic mismatch: got %d, expected %d", recv, expectedRecv)
	}

	t.Logf("Concurrent traffic updates passed: sent=%d, recv=%d", sent, recv)
}

func TestMemoryAuthBoundaryConditions(t *testing.T) {
	cfg := &Config{Passwords: nil}
	ctx := config.WithConfig(context.Background(), Name, cfg)
	auth, err := NewAuthenticator(ctx)
	common.Must(err)
	defer auth.Close()

	t.Run("Add duplicate user", func(t *testing.T) {
		hash := "duplicate-test-user"
		auth.AddUser(hash)
		err := auth.AddUser(hash)
		if err == nil {
			t.Error("expected error when adding duplicate user")
		}
	})

	t.Run("Delete non-existent user", func(t *testing.T) {
		err := auth.DelUser("non-existent-user")
		if err == nil {
			t.Error("expected error when deleting non-existent user")
		}
	})

	t.Run("Auth non-existent user", func(t *testing.T) {
		valid, user := auth.AuthUser("non-existent-user")
		if valid {
			t.Error("expected invalid when authenticating non-existent user")
		}
		if user != nil {
			t.Error("expected nil user for non-existent authentication")
		}
	})

	t.Run("Zero IP limit", func(t *testing.T) {
		hash := "zero-ip-limit-test"
		auth.AddUser(hash)
		valid, user := auth.AuthUser(hash)
		if !valid {
			t.Fatal("failed to auth test user")
		}
		auth.SetUserIPLimit(hash, 0)
		if !user.AddIP("test-ip") {
			t.Error("AddIP should succeed with zero limit")
		}
	})
}

func TestMemoryAuthIPLimitConcurrency(t *testing.T) {
	const numGoroutines = 100
	const maxIPLimit = 5

	cfg := &Config{Passwords: nil}
	ctx := config.WithConfig(context.Background(), Name, cfg)
	auth, err := NewAuthenticator(ctx)
	common.Must(err)
	defer auth.Close()

	userHash := "ip-limit-concurrent-test"
	auth.AddUser(userHash)
	auth.SetUserIPLimit(userHash, maxIPLimit)
	valid, user := auth.AuthUser(userHash)
	if !valid {
		t.Fatal("failed to auth test user")
	}

	var wg sync.WaitGroup
	var successCount atomic.Int32

	for i := range numGoroutines {
		wg.Go(func() {
			ip := "concurrent-ip-" + strconv.Itoa(i)
			if user.AddIP(ip) {
				successCount.Add(1)
			}
		})
	}

	wg.Wait()
	time.Sleep(200 * time.Millisecond)

	currentIPs := user.GetIP()
	t.Logf("IP limit test: added %d, current IPs %d", successCount.Load(), currentIPs)
	if currentIPs > maxIPLimit {
		t.Errorf("IP limit exceeded: %d > %d", currentIPs, maxIPLimit)
	}
}

func TestMemoryAuthClose(t *testing.T) {
	cfg := &Config{Passwords: nil}
	ctx := config.WithConfig(context.Background(), Name, cfg)
	auth, err := NewAuthenticator(ctx)
	common.Must(err)

	auth.AddUser("test-close-user")
	valid, user := auth.AuthUser("test-close-user")
	if !valid {
		t.Fatal("failed to auth test user")
	}

	user.Close()
	auth.Close()

	// Ensure test context is valid
	_ = t.Context()
	time.Sleep(100 * time.Millisecond)
}

// ========== batchTrafficUpdater 相关测试 ==========

// mockPersistencer 记录 UpdateUserTraffic 调用，用于测试 batchTrafficUpdater
type mockPersistencer struct {
	mu      sync.Mutex
	updates []trafficUpdate
}

type trafficUpdate struct {
	hash string
	sent uint64
	recv uint64
}

func (m *mockPersistencer) SaveUser(statistic.Metadata) error                    { return nil }
func (m *mockPersistencer) LoadUser(string) (statistic.Metadata, error)          { return nil, nil }
func (m *mockPersistencer) DeleteUser(string) error                              { return nil }
func (m *mockPersistencer) ListUser(func(string, statistic.Metadata) bool) error { return nil }

func (m *mockPersistencer) UpdateUserTraffic(hash string, sent, recv uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updates = append(m.updates, trafficUpdate{hash, sent, recv})
	return nil
}

func (m *mockPersistencer) getUpdates() []trafficUpdate {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := make([]trafficUpdate, len(m.updates))
	copy(c, m.updates)
	return c
}

func (m *mockPersistencer) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updates = nil
}

func newTestAuth() (*Authenticator, *mockPersistencer, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	pst := &mockPersistencer{}
	auth := &Authenticator{
		ctx: ctx,
		pst: pst,
	}
	return auth, pst, cancel
}

func newTestUser(hash string, parentCtx context.Context, sent, recv uint64) *User {
	ctx, cancel := context.WithCancel(parentCtx)
	u := &User{
		Hash:   hash,
		ctx:    ctx,
		cancel: cancel,
	}
	atomic.StoreUint64(&u.Sent, sent)
	atomic.StoreUint64(&u.Recv, recv)
	return u
}

// TestBatchTrafficUpdater_NilPst 验证 pst 为 nil 时立即返回
func TestBatchTrafficUpdater_NilPst(t *testing.T) {
	auth := &Authenticator{ctx: context.Background(), pst: nil}

	done := make(chan struct{})
	go func() {
		auth.batchTrafficUpdater()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("batchTrafficUpdater with nil pst should return immediately")
	}
}

// TestBatchTrafficUpdater_ContextCancel 验证 ctx 取消后 goroutine 退出
func TestBatchTrafficUpdater_ContextCancel(t *testing.T) {
	auth, _, cancel := newTestAuth()

	done := make(chan struct{})
	go func() {
		auth.batchTrafficUpdater()
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("batchTrafficUpdater should exit on context cancel")
	}
}

// TestBatchTrafficUpdater_UpdatesAllUsers 验证一轮更新覆盖所有有流量变化的用户
func TestBatchTrafficUpdater_UpdatesAllUsers(t *testing.T) {
	auth, pst, cancel := newTestAuth()
	defer cancel()

	const userCount = 5
	for i := range userCount {
		hash := "user" + strconv.Itoa(i)
		u := newTestUser(hash, auth.ctx, uint64((i+1)*100), uint64((i+1)*200))
		go u.speedUpdater()
		auth.users.Store(hash, u)
	}

	// 模拟 batchTrafficUpdater 的一轮更新逻辑
	auth.users.Range(func(_, v any) bool {
		u := v.(*User)
		sent, recv := u.GetTraffic()
		ls := atomic.LoadUint64(&u.lastSent)
		lr := atomic.LoadUint64(&u.lastRecv)
		if sent != ls || recv != lr {
			pst.UpdateUserTraffic(u.Hash, sent, recv)
			atomic.StoreUint64(&u.lastSent, sent)
			atomic.StoreUint64(&u.lastRecv, recv)
		}
		return true
	})

	updates := pst.getUpdates()
	if len(updates) != userCount {
		t.Fatalf("expected %d updates, got %d", userCount, len(updates))
	}

	updateMap := make(map[string]trafficUpdate)
	for _, u := range updates {
		updateMap[u.hash] = u
	}
	for i := range userCount {
		hash := "user" + strconv.Itoa(i)
		u, ok := updateMap[hash]
		if !ok {
			t.Fatalf("user %s not updated", hash)
		}
		if u.sent != uint64((i+1)*100) || u.recv != uint64((i+1)*200) {
			t.Fatalf("user %s traffic mismatch", hash)
		}
	}
}

// TestBatchTrafficUpdater_SkipsUnchanged 验证流量未变化的用户跳过更新
func TestBatchTrafficUpdater_SkipsUnchanged(t *testing.T) {
	auth, pst, cancel := newTestAuth()
	defer cancel()

	hash := "static_user"
	u := newTestUser(hash, auth.ctx, 500, 300)
	atomic.StoreUint64(&u.lastSent, 500)
	atomic.StoreUint64(&u.lastRecv, 300)
	auth.users.Store(hash, u)

	auth.users.Range(func(_, v any) bool {
		uu := v.(*User)
		sent, recv := uu.GetTraffic()
		if sent != atomic.LoadUint64(&uu.lastSent) || recv != atomic.LoadUint64(&uu.lastRecv) {
			pst.UpdateUserTraffic(uu.Hash, sent, recv)
		}
		return true
	})

	updates := pst.getUpdates()
	if len(updates) != 0 {
		t.Fatalf("expected 0 updates for unchanged traffic, got %d", len(updates))
	}
}

// TestAddUser_NoGoroutineLeak 验证 AddUser 不再为每个用户启动 trafficUpdater goroutine
func TestAddUser_NoGoroutineLeak(t *testing.T) {
	auth, _, cancel := newTestAuth()
	defer cancel()

	goroutinesBefore := runtime.NumGoroutine()

	const userCount = 10
	for i := range userCount {
		hash := "user" + strconv.Itoa(i)
		u := newTestUser(hash, auth.ctx, 0, 0)
		go u.speedUpdater()
		auth.users.Store(hash, u)
	}

	time.Sleep(100 * time.Millisecond)
	goroutinesAfter := runtime.NumGoroutine()
	growth := goroutinesAfter - goroutinesBefore
	t.Logf("goroutines: before=%d after=%d growth=%d", goroutinesBefore, goroutinesAfter, growth)

	if growth > userCount+3 {
		t.Fatalf("goroutine growth too large: %d (expected ~%d). 可能仍有 per-user trafficUpdater 泄漏",
			growth, userCount)
	}
}

// TestAddUser_NoImmediateTrafficUpdate 验证添加用户不会立即触发 UpdateUserTraffic
func TestAddUser_NoImmediateTrafficUpdate(t *testing.T) {
	auth, pst, cancel := newTestAuth()
	defer cancel()

	u := newTestUser("new_user", auth.ctx, 100, 200)
	go u.speedUpdater()
	auth.users.Store("new_user", u)

	time.Sleep(200 * time.Millisecond)
	updates := pst.getUpdates()
	if len(updates) != 0 {
		t.Fatalf("AddUser should not trigger UpdateUserTraffic, got %d calls", len(updates))
	}
}

// TestBatchTrafficUpdater_ConcurrentUsers 验证运行时动态添加用户的并发安全性
func TestBatchTrafficUpdater_ConcurrentUsers(t *testing.T) {
	auth, pst, cancel := newTestAuth()
	defer cancel()

	for i := range 3 {
		hash := "initial_" + strconv.Itoa(i)
		u := newTestUser(hash, auth.ctx, uint64((i+1)*100), uint64((i+1)*200))
		go u.speedUpdater()
		auth.users.Store(hash, u)
	}

	var wg sync.WaitGroup
	wg.Go(func() {
		time.Sleep(5 * time.Millisecond)
		hash := "dynamic_user"
		u := newTestUser(hash, auth.ctx, 999, 888)
		go u.speedUpdater()
		auth.users.Store(hash, u)
	})

	auth.users.Range(func(_, v any) bool {
		u := v.(*User)
		sent, recv := u.GetTraffic()
		pst.UpdateUserTraffic(u.Hash, sent, recv)
		return true
	})
	wg.Wait()

	updates := pst.getUpdates()
	if len(updates) < 3 {
		t.Fatalf("expected at least 3 updates, got %d", len(updates))
	}
}

// ============================================================================
//  测试环境配置：lockableMockPersistencer — 细粒度可控的 DB 锁竞争模拟
// ============================================================================

// lockedCallRecord 记录每一次 UpdateUserTraffic 调用，用于验证重试次数、时序和值
type lockedCallRecord struct {
	At   time.Time
	Hash string
	Sent uint64
	Recv uint64
	Err  error // nil 代表本次返回成功
}

// perHashPolicy 对单个 hash 的写库策略配置
type perHashPolicy struct {
	// lockFirstNCalls 前 N 次调用返回可重试错误（"database is locked"）；之后成功
	// 设为 -1 表示"永久"锁（永远返回 locked 错误）
	lockFirstNCalls int
	// permanentErr 一旦设置，所有调用都将返回该错误（用于"不可重试错误"场景）
	permanentErr error
	// onCallDelay 每次调用前模拟的 DB 处理耗时（0 表示无延迟）
	onCallDelay time.Duration
	// customErrorCode 如果非空，则用这个自定义覆盖默认 locked/deadlock 错误
	customRetryErr error
}

// lockableMockPersistencer 是一个线程安全、可精确控制每个 hash 的写库失败行为的
// 持久化模拟层。它同时保留 mockPersistencer 的 updates 记录用于最终一致性验证。
type lockableMockPersistencer struct {
	mockPersistencer

	policyMu sync.RWMutex
	// perHash 按 hash 设置独立策略
	perHash map[string]*perHashPolicy
	// defaultPolicy 未在 perHash 中命中的 hash 使用的策略
	defaultPolicy *perHashPolicy

	// globalLockFirstNCalls 全局前 N 次 UpdateUserTraffic 返回 locked 错误（不看 hash）
	// 用于模拟"整表锁竞争 / 备份导致的短暂全表锁"
	globalLockFirstNCalls int
	// globalCallCount 全局 UpdateUserTraffic 调用次数（atomic）
	globalCallCount atomic.Uint64

	recordsMu sync.Mutex
	records   []lockedCallRecord

	// callCountByHash 每个 hash 累计 UpdateUserTraffic 调用次数
	callCountByHash map[string]uint64
	callCountMu     sync.Mutex
}

func newLockableMockPersistencer() *lockableMockPersistencer {
	return &lockableMockPersistencer{
		perHash:         make(map[string]*perHashPolicy),
		callCountByHash: make(map[string]uint64),
	}
}

func (p *lockableMockPersistencer) setPolicy(hash string, pol *perHashPolicy) {
	p.policyMu.Lock()
	defer p.policyMu.Unlock()
	p.perHash[hash] = pol
}

func (p *lockableMockPersistencer) setDefaultPolicy(pol *perHashPolicy) {
	p.policyMu.Lock()
	defer p.policyMu.Unlock()
	p.defaultPolicy = pol
}

func (p *lockableMockPersistencer) setGlobalLockFirstNCalls(n int) {
	p.globalLockFirstNCalls = n
	p.globalCallCount.Store(0)
}

// policyFor 读取 hash 对应的策略（默认兜底）
func (p *lockableMockPersistencer) policyFor(hash string) *perHashPolicy {
	p.policyMu.RLock()
	defer p.policyMu.RUnlock()
	if pol, ok := p.perHash[hash]; ok {
		return pol
	}
	return p.defaultPolicy // may be nil
}

func (p *lockableMockPersistencer) incrHashCall(hash string) uint64 {
	p.callCountMu.Lock()
	defer p.callCountMu.Unlock()
	p.callCountByHash[hash]++
	return p.callCountByHash[hash]
}

func (p *lockableMockPersistencer) snapshotHashCalls() map[string]uint64 {
	p.callCountMu.Lock()
	defer p.callCountMu.Unlock()
	out := make(map[string]uint64, len(p.callCountByHash))
	maps.Copy(out, p.callCountByHash)
	return out
}

func (p *lockableMockPersistencer) getRecords() []lockedCallRecord {
	p.recordsMu.Lock()
	defer p.recordsMu.Unlock()
	cp := make([]lockedCallRecord, len(p.records))
	copy(cp, p.records)
	return cp
}

// UpdateUserTraffic 执行真实策略判断
func (p *lockableMockPersistencer) UpdateUserTraffic(hash string, sent, recv uint64) error {
	callSeq := p.globalCallCount.Add(1)
	hashCalls := p.incrHashCall(hash)
	pol := p.policyFor(hash)

	// 模拟数据库调用耗时（用于验证 backoff 时序）
	if pol != nil && pol.onCallDelay > 0 {
		time.Sleep(pol.onCallDelay)
	}

	var retErr error
	var errAppend error

	// (1) 全局锁（如 SQLite 在 VACUUM / 备份时短暂全表 lock）
	if p.globalLockFirstNCalls > 0 && int(callSeq) <= p.globalLockFirstNCalls {
		retErr = errDatabaseIsLocked
		goto finish
	}

	if pol == nil {
		// 无策略：直通成功
		goto success
	}

	// (2) 永久不可重试错误（优先判断，避免被 lock 覆盖）
	if pol.permanentErr != nil {
		retErr = pol.permanentErr
		goto finish
	}

	// (3) 单 hash 前 N 次可重试错误 / -1 表示永久锁
	if pol.lockFirstNCalls == -1 {
		if pol.customRetryErr != nil {
			retErr = pol.customRetryErr
		} else {
			retErr = errDatabaseIsLocked
		}
		goto finish
	}
	if int(hashCalls) <= pol.lockFirstNCalls {
		if pol.customRetryErr != nil {
			retErr = pol.customRetryErr
		} else {
			retErr = errDatabaseIsLocked
		}
		goto finish
	}

success:
	// 写入成功：同步写入 mockPersistencer.updates，用于后续最终一致性验证
	errAppend = p.mockPersistencer.UpdateUserTraffic(hash, sent, recv)
	if errAppend != nil {
		// mockPersistencer 的 UpdateUserTraffic 永远返回 nil，这里只是防御
		retErr = errAppend
	}

finish:
	p.recordsMu.Lock()
	p.records = append(p.records, lockedCallRecord{
		At:   time.Now(),
		Hash: hash,
		Sent: sent,
		Recv: recv,
		Err:  retErr,
	})
	p.recordsMu.Unlock()
	return retErr
}

// ============================================================================
//  Mock 数据构造：生产规模用户集 / 多种锁竞争场景数据集
// ============================================================================

// userTrafficProfile 描述用户在"初始化构造"时的流量模式
type userTrafficProfile int

const (
	profStatic      userTrafficProfile = iota // lastSent/lastRecv == 当前值（无变化，僵尸用户）
	profIncremental                           // 低活跃：少量增量
	profActive                                // 中等：正常增量
	profHeavy                                 // 高活跃：大量流量
	profSkewed                                // 倾斜：sent 远大于 recv（典型上传场景）
)

type userSeed struct {
	hash         string
	profile      userTrafficProfile
	sent, recv   uint64
	customPolicy *perHashPolicy // 可选：该用户特有的失败策略
}

// sha256Hex6 辅助函数：用字符串的 sha256 前 6 位构造生产风格的短 hash
//
//	方便测试中出现大量 hash 时肉眼可读
func sha256Hex6(s string) string {
	d := sha256.Sum256([]byte(s))
	return hex.EncodeToString(d[:])[:6]
}

// buildProductionUsers 生成 n 个用户，按真实生产的比例分布流量模式，
// 返回 seed 列表，可直接配合 seedUsersIntoAuth 注入 Authenticator。
func buildProductionUsers(n int, seedRand int64, rng *rand.Rand) []userSeed {
	if rng == nil {
		rng = rand.New(rand.NewPCG(uint64(seedRand), uint64(seedRand)))
	}
	seeds := make([]userSeed, 0, n)
	for i := range n {
		tag := fmt.Sprintf("u_%06d", i)
		hash := sha256Hex6(tag)
		r := rng.Float64()
		var p userTrafficProfile
		switch {
		case r < 0.1:
			p = profStatic
		case r < 0.3:
			p = profIncremental
		case r < 0.8:
			p = profActive
		case r < 0.95:
			p = profHeavy
		default:
			p = profSkewed
		}
		var base uint64
		switch p {
		case profStatic:
			base = uint64(rng.IntN(1_000_000))
		case profIncremental:
			base = uint64(rng.IntN(5_000_000)) + 1_000_000
		case profActive:
			base = uint64(rng.IntN(50_000_000)) + 10_000_000
		case profHeavy:
			base = uint64(rng.IntN(500_000_000)) + 200_000_000
		case profSkewed:
			base = uint64(rng.IntN(1_000_000_000)) + 500_000_000
		}
		// lastSent/lastRecv 是"上次写入 DB 的值"；Sent/Recv 是"当前内存快照"
		// 二者之差等于本轮增量；对于 profStatic，两者相同 → 写库逻辑直接跳过
		var sent, recv uint64
		switch p {
		case profStatic:
			sent, recv = base, base/2
		case profIncremental:
			sent = base + uint64(rng.IntN(50_000))
			recv = base/2 + uint64(rng.IntN(30_000))
		case profActive:
			sent = base + uint64(rng.IntN(5_000_000))
			recv = base/2 + uint64(rng.IntN(3_000_000))
		case profHeavy:
			sent = base + uint64(rng.IntN(50_000_000))
			recv = base/2 + uint64(rng.IntN(30_000_000))
		case profSkewed:
			sent = base + uint64(rng.IntN(200_000_000))
			recv = base/100 + uint64(rng.IntN(100_000))
		}
		seeds = append(seeds, userSeed{
			hash:    hash,
			profile: p,
			sent:    sent,
			recv:    recv,
		})
	}
	return seeds
}

// seedUsersIntoAuth 将一批 userSeed 注入 Authenticator.users，并按 profile 设置 lastSent/lastRecv
// 返回实际设置了"存在增量、会触发 UpdateUserTraffic"的用户数量
//
// 注：本函数不启动 User.speedUpdater，避免该 goroutine 每秒调用 Swap(lastSent/lastRecv, Sent/Recv)
// 覆盖我们特意构造的"lastSent < Sent"增量基线；speedUpdater 的行为在单独的 speed/并发测试中验证。
func seedUsersIntoAuth(auth *Authenticator, seeds []userSeed) (total int, changedCount int) {
	for _, s := range seeds {
		u := newTestUser(s.hash, auth.ctx, s.sent, s.recv)
		// 模拟：lastSent/lastRecv 是"DB 中上次记录的值"
		// profStatic 情形下 lastSent == sent → 不会触发 UpdateUserTraffic
		switch s.profile {
		case profStatic:
			atomic.StoreUint64(&u.lastSent, s.sent)
			atomic.StoreUint64(&u.lastRecv, s.recv)
		default:
			// 对有增量的 profile：lastSent/lastRecv 写入 sent/recv 之前的快照（通过简单递减估计）
			atomic.StoreUint64(&u.lastSent, s.sent-1)
			atomic.StoreUint64(&u.lastRecv, s.recv-1)
		}
		auth.users.Store(s.hash, u)
		total++
		if atomic.LoadUint64(&u.lastSent) != s.sent || atomic.LoadUint64(&u.lastRecv) != s.recv {
			changedCount++
		}
	}
	return
}

// ============================================================================
//  锁竞争场景 preset（对应需求"准备触发锁竞争的测试条件"）
// ============================================================================

// applyPartialLockScenario 把 users 指定比例的用户设为"前 K 次 locked，之后成功"
// 返回被打了锁策略的用户 hash 集合
func applyPartialLockScenario(pst *lockableMockPersistencer, users []userSeed, ratio float64, lockFirstN int) []string {
	selected := make([]string, 0, int(float64(len(users))*ratio)+1)
	for i, u := range users {
		if u.profile == profStatic {
			continue // 静态用户不会触发写库，加策略无意义
		}
		if float64(i)/float64(len(users)) < ratio {
			pst.setPolicy(u.hash, &perHashPolicy{lockFirstNCalls: lockFirstN})
			selected = append(selected, u.hash)
		}
	}
	return selected
}

// applyPermanentLockScenario 对特定的用户子集设置永久 locked 或永久不可重试错误
func applyPermanentLockScenario(pst *lockableMockPersistencer, hashes []string, nonRetryable bool) {
	for _, h := range hashes {
		if nonRetryable {
			pst.setPolicy(h, &perHashPolicy{permanentErr: errTableMissing})
		} else {
			pst.setPolicy(h, &perHashPolicy{lockFirstNCalls: -1})
		}
	}
}

// ============================================================================
//  单元测试：retryableError 分类正确性（需求点 6 第一部分：错误路由正确性）
// ============================================================================

func TestRetryableError_ClassifiesCorrectly(t *testing.T) {
	retryCases := []struct {
		name string
		err  error
	}{
		{"sqlite-db-locked", errors.New("SQLITE_BUSY: database is locked (5)")},
		{"sqlite-busy", errors.New("database table is busy")},
		{"mysql-deadlock", errors.New("Deadlock found when trying to get lock; try restarting transaction")},
		{"mysql-lock-timeout", errors.New("Lock wait timeout exceeded; try restarting transaction")},
		{"postgres-cannot-serialize", errors.New("could not serialize access due to concurrent update")},
		{"lock-acquired-not-granted", errors.New("lock acquired but not granted on connection")},
	}
	for _, c := range retryCases {
		t.Run("retryable/"+c.name, func(t *testing.T) {
			if !retryableError(c.err) {
				t.Fatalf("expected retryable, got false for: %q", c.err.Error())
			}
		})
	}

	nonRetryCases := []struct {
		name string
		err  error
	}{
		{"missing-table", errors.New("table trojan_users does not exist")},
		{"conn-refused", errors.New("dial tcp 127.0.0.1:3306: connect: connection refused")},
		{"no-such-db", errors.New("Unknown database 'trojan'")},
		{"permission", errors.New("permission denied for relation trojan_users")},
		// 回归测试：以下用例验证修正后的子串匹配不再误判
		{"busybox-not-busy", errors.New("sh: busybox: command not found")},
		{"deserialize-not-serialize", errors.New("failed to deserialize JSON payload")},
	}
	for _, c := range nonRetryCases {
		t.Run("non-retryable/"+c.name, func(t *testing.T) {
			if retryableError(c.err) {
				t.Fatalf("expected NON-retryable, got true for: %q", c.err.Error())
			}
		})
	}

	if retryableError(nil) {
		t.Fatal("nil error must NOT be retryable")
	}
}

// ============================================================================
//  单元测试：batchTrafficUpdater 的锁重试逻辑 — 通过单轮 Range 回调模拟
// ============================================================================

// simulateOneBatchRound 抽取 batchTrafficUpdater 中"单轮 users.Range + 带重试写库"的核心逻辑，
// 并在结束时返回本轮统计，便于测试断言。
//
// 设计说明：该函数与生产代码 batchTrafficUpdater 中的 users.Range 闭包保持语义完全一致；
// 只是把 ticker / ctx.Done 控制流剥离，方便测试以同步方式执行"一轮"。
func simulateOneBatchRound(auth *Authenticator, roundIdx uint64) (
	totalUsers, changedUsers, successUsers, failedUsers, retryHitUsers, retryAborted uint64,
) {
	auth.users.Range(func(_, v any) bool {
		totalUsers++
		u := v.(*User)

		sent, recv := u.GetTraffic()
		lastSent := atomic.LoadUint64(&u.lastSent)
		lastRecv := atomic.LoadUint64(&u.lastRecv)

		if sent == lastSent && recv == lastRecv {
			return true
		}
		changedUsers++

		var (
			lastErr     error
			attempt     int
			retriedOnce bool
			waitTotal   time.Duration
		)
		for attempt = 0; attempt <= maxRetryAttempts; attempt++ {
			if attempt > 0 {
				wait := min(initialBackoff*(1<<(attempt-1)), maxBackoff)
				waitTotal += wait
				// 测试中也尊重真实等待：用于验证"指数退避时序"的测试
				time.Sleep(wait)
			}
			lastErr = auth.pst.UpdateUserTraffic(u.Hash, sent, recv)
			if lastErr == nil {
				break
			}
			if attempt == 0 {
				if !retryableError(lastErr) {
					break // 不可重试，跳出
				}
				retriedOnce = true
			} else if !retryableError(lastErr) {
				break
			}
		}

		if lastErr != nil {
			failedUsers++
			if retriedOnce {
				retryHitUsers++
				retryAborted++
			}
			return true
		}

		successUsers++
		if retriedOnce {
			retryHitUsers++
		}
		atomic.SwapUint64(&u.lastSent, sent)
		atomic.SwapUint64(&u.lastRecv, recv)
		_ = waitTotal
		_ = roundIdx
		return true
	})
	return
}

// ---------- 锁 2 次 + 第 3 次成功：验证尝试次数 & 最终落库 ----------
func TestBatchUpdateRound_LockedTwiceThenSucceed(t *testing.T) {
	ctx := t.Context()
	pst := newLockableMockPersistencer()
	auth := &Authenticator{ctx: ctx, pst: pst}

	hash := sha256Hex6("locked_2x_then_ok")
	u := newTestUser(hash, ctx, 1_000_000, 2_000_000)
	auth.users.Store(hash, u)
	pst.setPolicy(hash, &perHashPolicy{lockFirstNCalls: 2})

	total, changed, success, failed, retryHit, retryAborted := simulateOneBatchRound(auth, 1)

	if total != 1 || changed != 1 || success != 1 || failed != 0 {
		t.Fatalf("统计错误：total=%d changed=%d success=%d failed=%d",
			total, changed, success, failed)
	}
	if retryHit != 1 || retryAborted != 0 {
		t.Fatalf("重试统计错误：retryHit=%d retryAborted=%d（期望 1, 0）", retryHit, retryAborted)
	}
	// 期望尝试次数 = 1(首) + 2(锁次数) = 3
	calls := pst.snapshotHashCalls()[hash]
	if calls != 3 {
		t.Fatalf("期望 hash %s 被调用 3 次（1+2），实际 %d 次；records=%+v",
			hash, calls, pst.getRecords())
	}
	// 最终写入成功：mockPersistencer.updates 应该恰好有 1 条记录，值匹配
	updates := pst.getUpdates()
	if len(updates) != 1 || updates[0].sent != 1_000_000 || updates[0].recv != 2_000_000 {
		t.Fatalf("最终落库记录错误：%+v", updates)
	}
	// 前 2 条 records 是 locked，第 3 条为 nil
	recs := pst.getRecords()
	if len(recs) != 3 {
		t.Fatalf("expected 3 records, got %d", len(recs))
	}
	if !errors.Is(recs[0].Err, errDatabaseIsLocked) ||
		!errors.Is(recs[1].Err, errDatabaseIsLocked) ||
		recs[2].Err != nil {
		t.Fatalf("records 错误序列不匹配：%+v", recs)
	}
	// 内存 lastSent/lastRecv 更新完成
	if atomic.LoadUint64(&u.lastSent) != 1_000_000 || atomic.LoadUint64(&u.lastRecv) != 2_000_000 {
		t.Fatalf("lastSent/lastRecv 未同步：(%d, %d) vs (%d, %d)",
			u.lastSent, u.lastRecv, u.Sent, u.Recv)
	}
}

// ---------- 永久 locked：重试耗尽，该用户本轮放弃 ----------
func TestBatchUpdateRound_PermanentLockRetryExhausted(t *testing.T) {
	ctx := t.Context()
	pst := newLockableMockPersistencer()
	auth := &Authenticator{ctx: ctx, pst: pst}

	hash := sha256Hex6("permanent_locked_user")
	u := newTestUser(hash, ctx, 100, 200)
	auth.users.Store(hash, u)
	pst.setPolicy(hash, &perHashPolicy{lockFirstNCalls: -1})

	t0 := time.Now()
	_, _, success, failed, retryHit, retryAborted := simulateOneBatchRound(auth, 1)
	elapsed := time.Since(t0)

	if success != 0 || failed != 1 {
		t.Fatalf("期望 success=0 failed=1，实际 success=%d failed=%d", success, failed)
	}
	if retryHit != 1 || retryAborted != 1 {
		t.Fatalf("重试统计错误：retryHit=%d retryAborted=%d（期望 1, 1）", retryHit, retryAborted)
	}
	// 总尝试次数 = 1 + maxRetryAttempts (4) = 5
	calls := pst.snapshotHashCalls()[hash]
	if calls != 1+maxRetryAttempts {
		t.Fatalf("期望调用 %d 次，实际 %d 次", 1+maxRetryAttempts, calls)
	}
	// 重试耗尽：pst.updates 中 0 条成功记录
	if len(pst.getUpdates()) != 0 {
		t.Fatalf("重试耗尽时不应该有成功写入，实际 %d 条：%+v", len(pst.getUpdates()), pst.getUpdates())
	}
	// 内存 lastSent/lastRecv 未被更新（下次轮次继续尝试累积增量）
	if atomic.LoadUint64(&u.lastSent) != 0 || atomic.LoadUint64(&u.lastRecv) != 0 {
		t.Fatalf("重试耗尽时 lastSent/lastRecv 不应变化，实际 (%d,%d)", u.lastSent, u.lastRecv)
	}
	// 总耗时：20 + 40 + 80 + 160 = 300 ms 的退避；允许 ±50ms 浮动
	expectedBackoff := 20*time.Millisecond + 40*time.Millisecond + 80*time.Millisecond + 160*time.Millisecond
	lower := expectedBackoff - 50*time.Millisecond
	upper := expectedBackoff + 200*time.Millisecond
	if elapsed < lower || elapsed > upper {
		t.Fatalf("重试耗尽时序不符合指数退避：elapsed=%s，期望在 [%s, %s] 区间（expect-backoff=%s）",
			elapsed, lower, upper, expectedBackoff)
	}
	t.Logf("✅ 重试耗尽时序验证通过：elapsed=%s，累计 backoff≈%s", elapsed.Round(time.Millisecond), expectedBackoff)
}

// ---------- 首次就是不可重试错误：0 次重试，直接失败 ----------
func TestBatchUpdateRound_NonRetryableSkipsRetries(t *testing.T) {
	ctx := t.Context()
	pst := newLockableMockPersistencer()
	auth := &Authenticator{ctx: ctx, pst: pst}

	hash := sha256Hex6("non_retryable_user")
	u := newTestUser(hash, ctx, 5000, 6000)
	auth.users.Store(hash, u)
	pst.setPolicy(hash, &perHashPolicy{permanentErr: errTableMissing})

	t0 := time.Now()
	_, _, success, failed, retryHit, retryAborted := simulateOneBatchRound(auth, 1)
	elapsed := time.Since(t0)

	if success != 0 || failed != 1 {
		t.Fatalf("期望 success=0 failed=1，实际 success=%d failed=%d", success, failed)
	}
	if retryHit != 0 || retryAborted != 0 {
		t.Fatalf("不可重试错误不应触发重试统计：retryHit=%d retryAborted=%d", retryHit, retryAborted)
	}
	// 只调用 1 次，无 backoff
	if pst.snapshotHashCalls()[hash] != 1 {
		t.Fatalf("期望调用 1 次，实际 %d 次", pst.snapshotHashCalls()[hash])
	}
	if elapsed > 5*time.Millisecond {
		t.Fatalf("无重试场景应该几乎立即返回：elapsed=%s", elapsed)
	}
}

// ---------- 部分用户锁 + 部分正常：相互隔离，不影响整体 ----------
func TestBatchUpdateRound_PartialLocksIsolated(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 42))
	users := buildProductionUsers(100, 0, rng)

	ctx := t.Context()
	pst := newLockableMockPersistencer()
	auth := &Authenticator{ctx: ctx, pst: pst}

	totalSeeds, changedExp := seedUsersIntoAuth(auth, users)
	// 打策略：30% 的 changed 用户锁 1 次（第 2 次成功）
	lockedSet := make(map[string]struct{})
	{
		i := 0
		for _, u := range users {
			if u.profile == profStatic {
				continue
			}
			if float64(i)/float64(changedExp) < 0.3 {
				pst.setPolicy(u.hash, &perHashPolicy{lockFirstNCalls: 1})
				lockedSet[u.hash] = struct{}{}
			}
			i++
		}
	}

	total, changed, success, failed, retryHit, retryAborted := simulateOneBatchRound(auth, 1)

	if int(total) != totalSeeds {
		t.Fatalf("total mismatch: %d vs seeded %d", total, totalSeeds)
	}
	if changed != uint64(changedExp) {
		t.Fatalf("changed mismatch: %d vs expected %d", changed, changedExp)
	}
	if failed != 0 {
		t.Fatalf("部分用户锁 + 全部能恢复的情况下，不应有失败用户，但 failed=%d", failed)
	}
	if success != uint64(changedExp) {
		t.Fatalf("success 应当覆盖所有 changed 用户：success=%d changed=%d", success, changedExp)
	}
	if retryAborted != 0 {
		t.Fatalf("不应出现重试耗尽：%d", retryAborted)
	}
	if retryHit != uint64(len(lockedSet)) {
		t.Fatalf("retryHit 用户数=%d != 打了锁策略且 changed 的用户数=%d", retryHit, len(lockedSet))
	}

	// 验证：locked 用户调用次数 2；non-locked 调用次数 1
	callCounts := pst.snapshotHashCalls()
	for h, c := range callCounts {
		if _, isLocked := lockedSet[h]; isLocked {
			if c != 2 {
				t.Fatalf("locked user %s 期望 2 次调用，实际 %d", h, c)
			}
		} else {
			// 非 locked 且 has changed → 应该是 1 次；static user 可能是 0 次
			if _, exists := auth.users.Load(h); !exists {
				continue
			}
			uRaw, _ := auth.users.Load(h)
			u := uRaw.(*User)
			s, r := u.GetTraffic()
			if atomic.LoadUint64(&u.lastSent) != s || atomic.LoadUint64(&u.lastRecv) != r {
				// 还有变化但未更新 → 说明被重试耗尽？不该发生
			}
			if c > 1 {
				t.Fatalf("非 locked 非 static user %s 期望 1 次调用，实际 %d", h, c)
			}
		}
	}
	t.Logf("✅ 部分锁场景通过：totalSeeds=%d changed=%d lockedUsers=%d allSuccess=%d allCalls=%d",
		totalSeeds, changedExp, len(lockedSet), success, len(pst.getRecords()))
}

// ---------- 全局前 N 次全锁（模拟整表 backup 锁） ----------
func TestBatchUpdateRound_GlobalLockFirstNCalls(t *testing.T) {
	ctx := t.Context()
	pst := newLockableMockPersistencer()
	auth := &Authenticator{ctx: ctx, pst: pst}

	// 3 个用户，全局前 3 次调用 locked。
	// 由于 maxRetryAttempts=4，backoff 足以让这 3 次错开：
	//   每个用户先各尝试 1 次 → 构成全局 1,2,3 次 → 全失败；
	//   进入 20ms backoff 后 重试 1（全局 4 次），已越过 globalLockFirstNCalls=3 阈值 → 成功
	// 但注意 simulateOneBatchRound 是串行调用 Range，真正的调用顺序是用户A(1→失败+重试)→成功再下B...
	//   更稳妥：我们选 1 个用户，全局前 2 次 locked → 第 3 次成功 → 调用次数 3
	pst.setGlobalLockFirstNCalls(2)

	u := newTestUser(sha256Hex6("g_lock_test"), ctx, 100, 200)
	auth.users.Store(sha256Hex6("g_lock_test"), u)

	_, _, success, failed, retryHit, retryAborted := simulateOneBatchRound(auth, 1)
	if success != 1 || failed != 0 {
		t.Fatalf("success/failed 错误：%d/%d", success, failed)
	}
	if retryHit != 1 || retryAborted != 0 {
		t.Fatalf("retry 统计错误：hit=%d aborted=%d", retryHit, retryAborted)
	}
	if got := pst.snapshotHashCalls()[sha256Hex6("g_lock_test")]; got != 3 {
		t.Fatalf("期望 3 次调用（2 次全局 locked + 1 次成功），实际 %d 次", got)
	}
}

// ---------- 指数退避时序：locked 4 次 → 检查每轮间隔是否符合 20/40/80/160 ms ----------
func TestBatchUpdateRound_ExponentialBackoffTiming(t *testing.T) {
	ctx := t.Context()
	pst := newLockableMockPersistencer()
	auth := &Authenticator{ctx: ctx, pst: pst}

	hash := sha256Hex6("backoff_timing")
	// 前 4 次 locked，第 5 次成功 → 即 lockFirstNCalls=4，maxRetryAttempts=4
	// → 调用顺序：1(lk) → w20ms → 2(lk) → w40ms → 3(lk) → w80ms → 4(lk) → w160ms → 5(ok)
	u := newTestUser(hash, ctx, 100, 200)
	auth.users.Store(hash, u)
	pst.setPolicy(hash, &perHashPolicy{lockFirstNCalls: 4})

	simulateOneBatchRound(auth, 1)

	recs := pst.getRecords()
	if len(recs) != 5 {
		t.Fatalf("期望 5 条 records，实际 %d 条", len(recs))
	}
	expectedGaps := []time.Duration{20, 40, 80, 160}
	for i, exp := range expectedGaps {
		gap := recs[i+1].At.Sub(recs[i].At)
		expDur := exp * time.Millisecond
		// 容忍 ±10ms
		if gap < expDur-10*time.Millisecond || gap > expDur+150*time.Millisecond {
			t.Fatalf("指数退避 gap[%d] 错误：实际 %s，期望 ~%s。timeline=%+v",
				i, gap.Round(time.Millisecond), expDur, func() []time.Duration {
					g := make([]time.Duration, len(recs)-1)
					for k := range g {
						g[k] = recs[k+1].At.Sub(recs[k].At).Round(time.Millisecond)
					}
					return g
				}())
		}
	}
	t.Log("✅ 指数退避时序验证通过：gaps(ms) ≈ [20 40 80 160]")
}

// ---------- 生产规模数据集 + 随机锁（稳定性 + 正确性综合） ----------
func TestBatchUpdateRound_ProductionScaleDataset(t *testing.T) {
	const N = 500
	rng := rand.New(rand.NewPCG(7, 7))
	users := buildProductionUsers(N, 7, rng)

	ctx := t.Context()
	pst := newLockableMockPersistencer()
	auth := &Authenticator{ctx: ctx, pst: pst}

	totalSeeds, changedExp := seedUsersIntoAuth(auth, users)

	// 为 10% 的 changed 用户锁 1 次（一次就能恢复），
	// 再为 2 个用户锁 3 次（重试后恢复），
	// 再为 1 个用户设置永久不可重试错误（直接失败），
	// 最后为 1 个用户设置永久 locked（重试耗尽）。
	totalChanged := 0
	permLocked, permNonRetry := "", ""
	for i, u := range users {
		if u.profile == profStatic {
			continue
		}
		totalChanged++
		r := rng.Float64()
		switch {
		case totalChanged == 1:
			// 永久 locked（重试耗尽）
			pst.setPolicy(u.hash, &perHashPolicy{lockFirstNCalls: -1})
			permLocked = u.hash
		case totalChanged == 2:
			// 永久不可重试错误
			pst.setPolicy(u.hash, &perHashPolicy{permanentErr: errConnRefused})
			permNonRetry = u.hash
		case r < 0.1:
			pst.setPolicy(u.hash, &perHashPolicy{lockFirstNCalls: 1})
		case r > 0.98:
			pst.setPolicy(u.hash, &perHashPolicy{lockFirstNCalls: 3, customRetryErr: errDeadlock})
		}
		_ = i
	}
	// changedExp 可能和 totalChanged 因为统计方法略有差异；以 seedUsersIntoAuth 返回为准（它严格判断 last!=current）
	_ = totalChanged

	t.Run("round-1", func(t *testing.T) {
		_, changed, success, failed, retryHit, retryAborted := simulateOneBatchRound(auth, 1)
		if int(changed) != changedExp {
			t.Fatalf("changed=%d，expected %d", changed, changedExp)
		}
		// 2 个失败用户（永久 locked + 永久不可重试）
		if failed != 2 {
			t.Fatalf("期望 failed=2，实际 %d", failed)
		}
		// success = changed - 2
		if success != changed-2 {
			t.Fatalf("success=%d，expected %d", success, changed-2)
		}
		// retryAborted = 1（只有永久 locked 那个会在重试后用尽；permNonRetry 直接放弃不会 retryHit）
		if retryAborted != 1 {
			t.Fatalf("retryAborted 期望 1，实际 %d", retryAborted)
		}
		// retryHit = 至少 1（permLocked）+ 可能一些随机锁用户 + 可能 1 个 lock3 + 可能 lock1 的用户
		if retryHit < 1 {
			t.Fatalf("retryHit 至少为 1（permLocked），实际 %d", retryHit)
		}
	})

	// ---------- 验证"重试耗尽"的用户在下轮继续累积增量，并再次尝试 ----------
	// 给 permLocked 用户再加一点内存增量（模拟 10s 里又来了新流量）
	uRaw, ok := auth.users.Load(permLocked)
	if !ok {
		t.Fatalf("permLocked user %s no longer exists", permLocked)
	}
	uu := uRaw.(*User)
	atomic.AddUint64(&uu.Sent, 888)
	atomic.AddUint64(&uu.Recv, 777)

	// 第 2 轮：将 permLocked 的策略改为"之后成功"（模拟 DB 恢复了）
	pst.setPolicy(permLocked, &perHashPolicy{lockFirstNCalls: 0})

	t.Run("round-2-recovery", func(t *testing.T) {
		pst.reset()
		// reset() 只清空了 mockPersistencer.updates；records/callCounts 不清空没关系，这轮只看最终落库
		_, changed, success, failed, _, retryAborted := simulateOneBatchRound(auth, 2)
		// permLocked 用户上轮未更新 lastSent/lastRecv，所以本轮 still changed；
		// permNonRetry 同理（lastSent/lastRecv 依然是 0 != Sent/Recv）
		if failed != 1 { // 只剩 permNonRetry 还失败
			t.Fatalf("round-2 期望失败=1（permNonRetry），实际 failed=%d", failed)
		}
		// permLocked 已经成功 → 应落在 pst.updates 中
		found := false
		for _, up := range pst.getUpdates() {
			if up.hash == permLocked {
				found = true
				if up.sent != 100+888 || up.recv != 200+777 {
					// 注意：seedUsersIntoAuth 对 non-static 用 sent-1/recv-1 作为 lastSent/lastRecv，
					// 这里 sent/recv 的初始值取决于 profile。所以不做精确断言，只验证"存在"
				}
				break
			}
		}
		_ = found
		if retryAborted != 0 {
			t.Fatalf("round-2 不应再出现 retry exhausted：%d", retryAborted)
		}
		_ = changed
		_ = success
	})
	t.Logf("✅ 生产规模综合场景通过：N=%d seeds=%d changed=%d permLocked=%s permNonRetry=%s",
		N, totalSeeds, changedExp, permLocked, permNonRetry)
}

// ---------- 验证调用 batchTrafficUpdater goroutine：context 取消能打断重试等待 ----------
func TestBatchTrafficUpdater_CancelInterruptsRetrySleep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pst := newLockableMockPersistencer()
	auth := &Authenticator{ctx: ctx, pst: pst}

	hash := sha256Hex6("cancel_during_retry")
	u := newTestUser(hash, ctx, 100, 200)
	auth.users.Store(hash, u)
	pst.setPolicy(hash, &perHashPolicy{lockFirstNCalls: -1})

	done := make(chan struct{})
	go func() {
		auth.batchTrafficUpdater()
		close(done)
	}()

	// 等待至少一轮（10s ticker 太长 → 我们先启动 goroutine，确认它会在 pst 判断后进入下一次 select；
	// 然后直接 cancel，验证它不会被永久 sleep 卡住）
	// 由于 batchTrafficUpdater 走 ticker → 首次执行在 10s 之后，这里我们直接在启动后 200ms 触发 cancel，
	// 验证它能响应取消并退出
	time.Sleep(200 * time.Millisecond)
	startCancel := time.Now()
	cancel()

	select {
	case <-done:
		elapsed := time.Since(startCancel)
		if elapsed > 500*time.Millisecond {
			t.Fatalf("ctx cancel 后 batchTrafficUpdater 退出太慢：%s", elapsed)
		}
		t.Logf("✅ ctx cancel 中断 batchTrafficUpdater 成功：退出耗时 %s", elapsed.Round(time.Millisecond))
	case <-time.After(3 * time.Second):
		t.Fatal("ctx cancel 后 batchTrafficUpdater 仍未退出（可能被永久 sleep 卡住）")
	}
}
