package transport

import (
	"os"
	"strings"
	"testing"
)

func TestCloseResultIsPublishedAfterOnce(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("读取 server.go: %v", err)
	}
	text := string(src)
	if !strings.Contains(text, "closeDone chan struct{}") ||
		!strings.Contains(text, "defer close(s.closeDone)") ||
		!strings.Contains(text, "<-s.closeDone") {
		t.Fatal("Close 仍在 sync.Once 完成前返回 closeErr；并发 Close/accept 会读到未同步结果")
	}
}
