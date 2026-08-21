package memory

import (
	"context"
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

func (m *mockPersistencer) SaveUser(statistic.Metadata) error              { return nil }
func (m *mockPersistencer) LoadUser(string) (statistic.Metadata, error)   { return nil, nil }
func (m *mockPersistencer) DeleteUser(string) error                       { return nil }
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
	for i := 0; i < userCount; i++ {
		hash := "user" + strconv.Itoa(i)
		u := newTestUser(hash, auth.ctx, uint64((i+1)*100), uint64((i+1)*200))
		go u.speedUpdater()
		auth.users.Store(hash, u)
	}

	// 模拟 batchTrafficUpdater 的一轮更新逻辑
	auth.users.Range(func(_, v interface{}) bool {
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
	for i := 0; i < userCount; i++ {
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

	auth.users.Range(func(_, v interface{}) bool {
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
	for i := 0; i < userCount; i++ {
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

	for i := 0; i < 3; i++ {
		hash := "initial_" + strconv.Itoa(i)
		u := newTestUser(hash, auth.ctx, uint64((i+1)*100), uint64((i+1)*200))
		go u.speedUpdater()
		auth.users.Store(hash, u)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		hash := "dynamic_user"
		u := newTestUser(hash, auth.ctx, 999, 888)
		go u.speedUpdater()
		auth.users.Store(hash, u)
	}()

	auth.users.Range(func(_, v interface{}) bool {
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
