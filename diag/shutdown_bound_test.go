package diag

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestStopWaitsForServeExit(t *testing.T) {
	src, err := os.ReadFile("diag.go")
	if err != nil {
		t.Fatalf("读取 diag.go: %v", err)
	}
	text := string(src)
	if !strings.Contains(text, "serveDone := make(chan struct{})") ||
		!strings.Contains(text, "defer close(serveDone)") ||
		!strings.Contains(text, "<-serveDone") {
		t.Fatal("Start 返回的 stop 没有等待 Serve goroutine 退出；旧实现会留下短时孤儿协程")
	}

	_, stop, err := Start("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stop 在 2 秒内没有返回")
	}
}
