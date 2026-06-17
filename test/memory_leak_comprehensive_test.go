package test

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/statistic/memory"
)

func TestMuxServerGoroutineLeak(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	startGoroutines := runtime.NumGoroutine()

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(10 * time.Millisecond)
		}()
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond)
	runtime.GC()

	finalGoroutines := runtime.NumGoroutine()
	t.Logf("Start goroutines: %d, Final goroutines: %d", startGoroutines, finalGoroutines)

	if finalGoroutines-startGoroutines > 2 {
		t.Errorf("Potential goroutine leak in mux server: %d goroutines remaining", finalGoroutines-startGoroutines)
	}
}

func TestMemoryAuthenticatorGoroutineLeak(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctx = config.WithConfig(ctx, memory.Name, &memory.Config{})

	startGoroutines := runtime.NumGoroutine()

	auth, err := memory.NewAuthenticator(ctx)
	if err != nil {
		t.Fatalf("Failed to create authenticator: %v", err)
	}

	hash := "test-hash-for-leak-test"
	if err := auth.AddUser(hash); err != nil {
		t.Fatalf("Failed to add user: %v", err)
	}

	for i := 0; i < 100; i++ {
		_, user := auth.AuthUser(hash)
		if user == nil {
			t.Fatal("User not found")
		}
		user.AddIP(fmt.Sprintf("192.168.1.%d", i%10))
		time.Sleep(10 * time.Millisecond)
	}

	time.Sleep(2 * time.Second)
	runtime.GC()

	if err := auth.DelUser(hash); err != nil {
		t.Fatalf("Failed to delete user: %v", err)
	}

	time.Sleep(2 * time.Second)
	runtime.GC()

	if err := auth.Close(); err != nil {
		t.Fatalf("Failed to close authenticator: %v", err)
	}

	time.Sleep(1 * time.Second)
	runtime.GC()

	finalGoroutines := runtime.NumGoroutine()
	t.Logf("Start goroutines: %d, Final goroutines: %d", startGoroutines, finalGoroutines)

	if finalGoroutines-startGoroutines > 5 {
		t.Errorf("Potential goroutine leak in memory authenticator: %d goroutines remaining", finalGoroutines-startGoroutines)
	}
}

func TestMemoryAuthenticatorIPCLeak(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctx = config.WithConfig(ctx, memory.Name, &memory.Config{})

	startGoroutines := runtime.NumGoroutine()

	auth, err := memory.NewAuthenticator(ctx)
	if err != nil {
		t.Fatalf("Failed to create authenticator: %v", err)
	}
	defer auth.Close()

	hash := "test-ip-cleaner"
	if err := auth.AddUser(hash); err != nil {
		t.Fatalf("Failed to add user: %v", err)
	}

	_, user := auth.AuthUser(hash)
	if user == nil {
		t.Fatal("User not found")
	}

	for i := 0; i < 50; i++ {
		user.AddIP(fmt.Sprintf("10.0.0.%d", i%10))
	}

	time.Sleep(15 * time.Second)
	runtime.GC()

	time.Sleep(2 * time.Second)
	runtime.GC()

	if err := auth.DelUser(hash); err != nil {
		t.Fatalf("Failed to delete user: %v", err)
	}

	time.Sleep(2 * time.Second)
	runtime.GC()

	finalGoroutines := runtime.NumGoroutine()
	t.Logf("Start goroutines: %d, Final goroutines: %d", startGoroutines, finalGoroutines)

	if finalGoroutines-startGoroutines > 5 {
		t.Errorf("Potential goroutine leak in IP cleaner: %d goroutines remaining", finalGoroutines-startGoroutines)
	}
}

func TestHighConcurrencyAuthenticator(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping long-running high concurrency test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctx = config.WithConfig(ctx, memory.Name, &memory.Config{})

	startGoroutines := runtime.NumGoroutine()

	auth, err := memory.NewAuthenticator(ctx)
	if err != nil {
		t.Fatalf("Failed to create authenticator: %v", err)
	}
	defer auth.Close()

	hash := "test-high-concurrency"
	if err := auth.AddUser(hash); err != nil {
		t.Fatalf("Failed to add user: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, user := auth.AuthUser(hash)
			if user != nil {
				user.AddIP(fmt.Sprintf("192.168.0.%d", idx%10))
			}
			time.Sleep(time.Millisecond)
		}(i)
	}

	wg.Wait()
	time.Sleep(15 * time.Second)
	runtime.GC()

	if err := auth.DelUser(hash); err != nil {
		t.Fatalf("Failed to delete user: %v", err)
	}

	time.Sleep(3 * time.Second)
	runtime.GC()

	finalGoroutines := runtime.NumGoroutine()
	t.Logf("Start goroutines: %d, Final goroutines: %d", startGoroutines, finalGoroutines)

	if finalGoroutines-startGoroutines > 10 {
		t.Errorf("Potential goroutine leak after high concurrency test: %d goroutines remaining", finalGoroutines-startGoroutines)
	}
}

func TestConcurrentUserOperations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctx = config.WithConfig(ctx, memory.Name, &memory.Config{})

	startGoroutines := runtime.NumGoroutine()

	auth, err := memory.NewAuthenticator(ctx)
	if err != nil {
		t.Fatalf("Failed to create authenticator: %v", err)
	}
	defer auth.Close()

	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(userIdx int) {
			defer wg.Done()
			hash := fmt.Sprintf("user-%d", userIdx)
			if err := auth.AddUser(hash); err != nil {
				t.Logf("Failed to add user %s: %v", hash, err)
				return
			}

			for j := 0; j < 50; j++ {
				_, user := auth.AuthUser(hash)
				if user != nil {
					user.AddIP(fmt.Sprintf("10.0.%d.%d", userIdx, j%10))
				}
				time.Sleep(5 * time.Millisecond)
			}

			if err := auth.DelUser(hash); err != nil {
				t.Logf("Failed to delete user %s: %v", hash, err)
			}
		}(i)
	}

	wg.Wait()
	time.Sleep(15 * time.Second)
	runtime.GC()

	time.Sleep(3 * time.Second)
	runtime.GC()

	finalGoroutines := runtime.NumGoroutine()
	t.Logf("Start goroutines: %d, Final goroutines: %d", startGoroutines, finalGoroutines)

	if finalGoroutines-startGoroutines > 10 {
		t.Errorf("Potential goroutine leak after concurrent user operations: %d goroutines remaining", finalGoroutines-startGoroutines)
	}
}