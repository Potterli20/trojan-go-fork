package common

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestTFODialSuccess(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		conn.Close()
	}()

	cfg := DialConfig{
		Network:    "tcp",
		Address:    listener.Addr().String(),
		EnableTFO:  true,
		Timeout:    5 * time.Second,
		KeepAlive:  true,
		NoDelay:    true,
		RetryCount: 1,
	}

	conn, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	t.Logf("Connected successfully with TFO enabled")
}

func TestTFODisableDialSuccess(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		conn.Close()
	}()

	cfg := DialConfig{
		Network:    "tcp",
		Address:    listener.Addr().String(),
		EnableTFO:  false,
		Timeout:    5 * time.Second,
		KeepAlive:  true,
		NoDelay:    true,
		RetryCount: 1,
	}

	conn, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	t.Logf("Connected successfully with TFO disabled")
}

func TestTFODialWithRetry(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}

	addr := listener.Addr().String()

	time.AfterFunc(100*time.Millisecond, func() {
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}()
	})

	cfg := DialConfig{
		Network:       "tcp",
		Address:       addr,
		EnableTFO:     true,
		Timeout:       50 * time.Millisecond,
		KeepAlive:     true,
		NoDelay:       true,
		RetryCount:    2,
		RetryInterval: 50 * time.Millisecond,
	}

	conn, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Dial with retry failed: %v", err)
	}
	defer conn.Close()
	defer listener.Close()

	t.Logf("Connected successfully with retry")
}

func TestTFODialTimeout(t *testing.T) {
	cfg := DialConfig{
		Network:    "tcp",
		Address:    "127.0.0.1:1",
		EnableTFO:  true,
		Timeout:    100 * time.Millisecond,
		RetryCount: 0,
	}

	_, err := Dial(context.Background(), cfg)
	if err == nil {
		t.Fatal("Expected error for timeout, got nil")
	}

	t.Logf("Expected timeout error: %v", err)
}

// TestDialRetryWaitHonorsContext 钉住重试间隔对 ctx 的响应：ctx 已取消时，
// Dial 不能在两次尝试之间白等满 RetryInterval（那段时间调用方卡在关停里）。
// 判据取"总耗时"，不依赖具体错误文案：旧实现会睡 3×5s，新实现立即返回。
func TestDialRetryWaitHonorsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := DialConfig{
		Network:       "tcp",
		Address:       "127.0.0.1:1",
		Timeout:       100 * time.Millisecond,
		RetryCount:    3,
		RetryInterval: 5 * time.Second,
	}

	start := time.Now()
	if _, err := Dial(ctx, cfg); err == nil {
		t.Fatal("ctx 已取消，Dial 却成功返回")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("ctx 已取消，Dial 仍耗时 %s：重试间隔没有接 ctx", elapsed)
	}
}

func TestTFOListenSuccess(t *testing.T) {
	cfg := ListenConfig{
		EnableTFO: true,
	}

	listener, err := Listen(context.Background(), cfg, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer listener.Close()

	t.Logf("Listener created successfully with TFO enabled on %s", listener.Addr())
}

func TestTFOListenDisableSuccess(t *testing.T) {
	cfg := ListenConfig{
		EnableTFO: false,
	}

	listener, err := Listen(context.Background(), cfg, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer listener.Close()

	t.Logf("Listener created successfully with TFO disabled on %s", listener.Addr())
}

func TestDialPreferIPv4(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		conn.Close()
	}()

	cfg := DialConfig{
		Network:    "tcp",
		Address:    listener.Addr().String(),
		EnableTFO:  false,
		Timeout:    5 * time.Second,
		PreferIPv4: true,
	}

	conn, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	t.Logf("Connected successfully with PreferIPv4")
}

func TestDialKeepAliveAndNoDelay(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		conn.Close()
	}()

	cfg := DialConfig{
		Network:   "tcp",
		Address:   listener.Addr().String(),
		EnableTFO: false,
		Timeout:   5 * time.Second,
		KeepAlive: true,
		NoDelay:   true,
	}

	conn, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	if _, ok := conn.(*net.TCPConn); !ok {
		t.Fatal("Expected TCP connection")
	}

	t.Logf("Connection established successfully with KeepAlive and NoDelay options")
}
