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
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			userHash := "concurrent-user-" + strconv.Itoa(id)

			for range numOpsPerGoroutine {
				if err := auth.AddUser(userHash); err == nil {
					successCount.Add(1)
					auth.DelUser(userHash)
				}
			}
		}(i)
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
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ip := "concurrent-ip-" + strconv.Itoa(id)
			if user.AddIP(ip) {
				successCount.Add(1)
			}
		}(i)
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancel()
	time.Sleep(100 * time.Millisecond)
}
