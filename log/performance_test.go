package log

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
)

func BenchmarkConnectionTrackerHighConcurrency(b *testing.B) {
	SetLogLevelInternal(InfoLevel)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tracker := NewConnectionTracker("Benchmark", "Connect").
				WithField("host", "10.0.0.1").
				WithField("port", 8080)
			time.Sleep(time.Microsecond * 10)
			tracker.Success()
		}
	})
}

func BenchmarkConnectionTrackerDisabledLog(b *testing.B) {
	SetLogLevelInternal(OffLevel)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tracker := NewConnectionTracker("Benchmark", "Connect").
				WithField("host", "10.0.0.1").
				WithField("port", 8080)
			time.Sleep(time.Microsecond * 10)
			tracker.Success()
		}
	})
}

func TestHighConcurrencyConnections(t *testing.T) {
	const numConnections = 1000

	SetLogLevelInternal(InfoLevel)

	var wg sync.WaitGroup
	startTime := time.Now()

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	initialAlloc := memStats.Alloc

	t.Logf("Starting high concurrency test with %d connections", numConnections)

	wg.Add(numConnections)
	for i := 0; i < numConnections; i++ {
		go func(connID int) {
			defer wg.Done()

			tracker := NewConnectionTracker("Test", "Connect").
				WithField("conn_idx", connID).
				WithField("target", "10.0.0.1:8080")

			time.Sleep(time.Millisecond * time.Duration(rand.Intn(91)+10))

			if connID%100 == 0 {
				tracker.Error(fmt.Errorf("simulated error for conn %d", connID))
			} else {
				tracker.Success()
			}

			time.Sleep(time.Millisecond * time.Duration(rand.Intn(151)+50))

			tracker.Destroy("normal close", uint64(rand.Intn(9901)+100), uint64(rand.Intn(9901)+100))
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(startTime)

	runtime.ReadMemStats(&memStats)
	finalAlloc := memStats.Alloc
	memUsed := (finalAlloc - initialAlloc) / 1024

	t.Logf("=== High Concurrency Test Results ===")
	t.Logf("Connections: %d", numConnections)
	t.Logf("Duration: %v", elapsed)
	t.Logf("Memory Used: %d KB", memUsed)
	t.Logf("Throughput: %.2f connections/sec", float64(numConnections)/elapsed.Seconds())
}

func TestConnectionLifecycleWithStats(t *testing.T) {
	SetLogLevelInternal(AllLevel)

	tracker := NewConnectionTracker("Lifecycle", "Dial")
	t.Logf("Connection ID: %s", tracker.ConnID())
	t.Logf("Start Time: %s", tracker.StartTime().Format(time.RFC3339))

	time.Sleep(time.Millisecond * 50)
	tracker.Success()

	time.Sleep(time.Millisecond * 100)
	tracker.Destroy("test complete", 1500, 2500)

	t.Log("Connection lifecycle test completed")
}

func TestPerformanceComparison(t *testing.T) {
	testCases := []struct {
		name     string
		logLevel LogLevel
	}{
		{"Debug level", DebugLevel},
		{"Info level", InfoLevel},
		{"Error level", ErrorLevel},
		{"Off level", OffLevel},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			SetLogLevelInternal(tc.logLevel)

			const iterations = 10000
			start := time.Now()

			for i := 0; i < iterations; i++ {
				tracker := NewConnectionTracker("PerfTest", "Connect").
					WithField("id", i)
				time.Sleep(time.Nanosecond * 100)
				tracker.Success()
			}

			elapsed := time.Since(start)
			opsPerSec := float64(iterations) / elapsed.Seconds()

			t.Logf("Level: %v, Iterations: %d, Elapsed: %v, Ops/sec: %.2f",
				tc.logLevel, iterations, elapsed, opsPerSec)
		})
	}
}

func TestStressTestWithTimestamps(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	SetLogLevelInternal(InfoLevel)

	const numGoroutines = 100
	const iterationsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	start := time.Now()

	for i := 0; i < numGoroutines; i++ {
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < iterationsPerGoroutine; j++ {
				tracker := NewConnectionTracker("Stress", fmt.Sprintf("conn-%d-%d", goroutineID, j))
				tracker.Success()
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	totalOperations := numGoroutines * iterationsPerGoroutine
	throughput := float64(totalOperations) / elapsed.Seconds()

	t.Logf("=== Stress Test Results ===")
	t.Logf("Goroutines: %d", numGoroutines)
	t.Logf("Iterations per goroutine: %d", iterationsPerGoroutine)
	t.Logf("Total operations: %d", totalOperations)
	t.Logf("Elapsed: %v", elapsed)
	t.Logf("Throughput: %.2f ops/sec", throughput)
}

func TestMain(m *testing.M) {
	fmt.Println("=== Log Module Performance Test Suite ===")
	fmt.Println("Running unit tests and benchmarks...")
	fmt.Println()

	code := m.Run()

	fmt.Println()
	fmt.Println("=== Test Suite Completed ===")
	os.Exit(code)
}
