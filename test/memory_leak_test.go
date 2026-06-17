package test

import (
	"net"
	"runtime"
	"testing"
	"time"
)

func TestSimpleConnectionCycle(t *testing.T) {
	_ = t.Context() // Ensure test context is valid

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					_, err := c.Read(buf)
					if err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	for cycle := range 100 {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatalf("Cycle %d: Failed to connect: %v", cycle, err)
		}
		conn.Write([]byte("hello"))
		conn.Close()

		if cycle%10 == 0 {
			runtime.GC()
			t.Logf("Cycle %d: Goroutines: %d", cycle, runtime.NumGoroutine())
		}
	}

	runtime.GC()
	time.Sleep(time.Second)

	finalGoroutines := runtime.NumGoroutine()
	t.Logf("Final goroutine count after 100 cycles: %d", finalGoroutines)

	if finalGoroutines > 10 {
		t.Errorf("Potential goroutine leak: %d goroutines remaining", finalGoroutines)
	}
}

func TestConcurrentConnectionPool(t *testing.T) {
	const poolSize = 100
	const testCycles = 5

	_ = t.Context() // Ensure test context is valid

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					_, err := c.Read(buf)
					if err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	for cycle := range testCycles {
		var conns []net.Conn
		for i := range poolSize {
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatalf("Cycle %d, conn %d: Failed to connect: %v", cycle, i, err)
			}
			conns = append(conns, conn)
		}

		t.Logf("Cycle %d: Created %d connections", cycle, len(conns))

		for _, conn := range conns {
			conn.Close()
		}

		runtime.GC()
		time.Sleep(500 * time.Millisecond)

		t.Logf("Cycle %d: After cleanup - Goroutines: %d", cycle, runtime.NumGoroutine())
	}

	finalGoroutines := runtime.NumGoroutine()
	t.Logf("Final goroutine count: %d", finalGoroutines)

	if finalGoroutines > 10 {
		t.Errorf("Potential goroutine leak: %d goroutines remaining", finalGoroutines)
	}
}

func TestHighConcurrencyMemoryLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping long-running memory leak test in short mode")
	}

	const (
		numConnections = 1000
		testDuration   = 10 * time.Second
		waitDuration   = 5 * time.Second
	)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					_, err := c.Read(buf)
					if err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	t.Logf("Listener started on %s", addr)

	var connections []net.Conn
	for i := range numConnections {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatalf("Failed to connect (%d/%d): %v", i+1, numConnections, err)
		}
		connections = append(connections, conn)
		if (i+1)%100 == 0 {
			t.Logf("Connected %d/%d", i+1, numConnections)
		}
	}

	t.Logf("All %d connections established", numConnections)

	printMemoryStats(t, "After connections established")

	t.Logf("Waiting for %v...", testDuration)
	time.Sleep(testDuration)

	printMemoryStats(t, "After test duration")

	t.Logf("Closing all connections...")
	for i, conn := range connections {
		conn.Close()
		if (i+1)%100 == 0 {
			t.Logf("Closed %d/%d", i+1, numConnections)
		}
	}

	t.Logf("Waiting for %v to allow cleanup...", waitDuration)
	time.Sleep(waitDuration)

	runtime.GC()
	printMemoryStats(t, "After cleanup and GC")

	numGoroutines := runtime.NumGoroutine()
	t.Logf("Current goroutine count: %d", numGoroutines)

	if numGoroutines > 10 {
		t.Errorf("High goroutine count after cleanup: %d. This may indicate a goroutine leak.", numGoroutines)
	}
}

func printMemoryStats(t *testing.T, phase string) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	t.Logf("=== Memory Stats: %s ===", phase)
	t.Logf("  Alloc:         %.2f MB", float64(memStats.Alloc)/1024/1024)
	t.Logf("  TotalAlloc:    %.2f MB", float64(memStats.TotalAlloc)/1024/1024)
	t.Logf("  HeapAlloc:     %.2f MB", float64(memStats.HeapAlloc)/1024/1024)
	t.Logf("  HeapSys:       %.2f MB", float64(memStats.HeapSys)/1024/1024)
	t.Logf("  HeapObjects:   %d", memStats.HeapObjects)
	t.Logf("  Goroutines:    %d", runtime.NumGoroutine())
}
