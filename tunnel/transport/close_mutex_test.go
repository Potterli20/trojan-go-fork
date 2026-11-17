package transport

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
)

func TestCloseUsesMutexState(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("读取 server.go: %v", err)
	}
	text := string(src)
	// 字段声明会被 gofmt 按对齐列重排，断言不能写死列间空格数。
	compact := strings.Join(strings.Fields(text), "")
	if !strings.Contains(compact, "closeMusync.Mutex") ||
		!strings.Contains(compact, "closedbool") ||
		strings.Contains(compact, "closeOncesync.Once") ||
		strings.Contains(compact, "closeDonechanstruct{}") {
		t.Fatal("Close 仍依赖 sync.Once/closeDone；关停状态必须由互斥锁保护")
	}
	if !strings.Contains(text, "s.closeMu.Lock()") ||
		!strings.Contains(text, "defer s.closeMu.Unlock()") {
		t.Fatal("Close 没有用互斥锁发布关闭结果")
	}
}

// TestConcurrentFirstAcceptAndClose 对"首次 AcceptConn 启动 acceptLoop"与
// "Close 走到 wg.Wait()"交错的路径做并发压测，确保二者在 closeMu 下串行后
// 既无数据竞争也无 WaitGroup 误用。修复前这条路径缺少串行化；该竞态是
// 偶发的，此测试作为回归防护而非稳定复现器。必须带 -race 运行才有意义。
func TestConcurrentFirstAcceptAndClose(t *testing.T) {
	for range 20 {
		cfg := &Config{
			LocalHost:  "127.0.0.1",
			LocalPort:  common.PickPort("tcp", "127.0.0.1"),
			RemoteHost: "127.0.0.1",
			RemotePort: common.PickPort("tcp", "127.0.0.1"),
		}
		ctx := config.WithConfig(context.Background(), Name, cfg)
		s, err := NewServer(ctx, nil)
		if err != nil {
			t.Fatalf("NewServer: %v", err)
		}

		var wg sync.WaitGroup
		const callers = 8
		for range callers {
			wg.Go(func() {
				conn, err := s.AcceptConn(nil)
				if err == nil && conn != nil {
					conn.Close() //gosec:disable -- 错误忽略：测试清理
				}
			})
		}
		wg.Go(func() {
			s.Close()
		})
		wg.Wait()
	}
}
