package transport

import (
	"os"
	"strings"
	"testing"
)

func TestCloseUsesMutexState(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("读取 server.go: %v", err)
	}
	text := string(src)
	if !strings.Contains(text, "closeMu   sync.Mutex") ||
		!strings.Contains(text, "closed    bool") ||
		strings.Contains(text, "\tcloseOnce sync.Once") ||
		strings.Contains(text, "closeDone chan struct{}") {
		t.Fatal("Close 仍依赖 sync.Once/closeDone；关停状态必须由互斥锁保护")
	}
	if !strings.Contains(text, "s.closeMu.Lock()") ||
		!strings.Contains(text, "defer s.closeMu.Unlock()") {
		t.Fatal("Close 没有用互斥锁发布关闭结果")
	}
}
