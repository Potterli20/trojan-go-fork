package recorder

import (
	"net"
	"testing"
	"time"
)

// TestDuplicateSubscribeIsRejected 钉住同一 uid 的重复订阅：
// 旧实现用 Swap 就地替换，旧通道从此没人关闭——原订阅者永久阻塞在接收上，
// 而它自己的 goroutine 与缓冲也一直占着。现在必须显式报错，且不得干扰已有订阅。
func TestDuplicateSubscribeIsRejected(t *testing.T) {
	const uid = "duplicate-subscription"
	base := subscriberCount.Load()
	t.Cleanup(func() { Unsubscribe(uid) })

	first, err := Subscribe(uid, "", "", false)
	if err != nil {
		t.Fatalf("首次订阅失败: %v", err)
	}
	if got := subscriberCount.Load(); got != base+1 {
		t.Fatalf("首次订阅后计数 = %d，期望 %d", got, base+1)
	}

	second, err := Subscribe(uid, "", "", false)
	if err == nil {
		t.Fatal("重复 uid 的订阅被接受了：旧通道再没人关闭，原订阅者会永久阻塞在接收上")
	}
	if second != nil {
		t.Fatal("被拒的重复订阅却返回了非 nil 通道")
	}
	if got := subscriberCount.Load(); got != base+1 {
		t.Fatalf("重复订阅被拒之后计数 = %d，期望 %d（不能重复计数）", got, base+1)
	}

	// 原订阅必须还在、还能收记录：拒绝重复订阅不能顺手把旧通道关掉
	Add("hash", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40001},
		&net.TCPAddr{IP: net.IPv4(198, 51, 100, 1), Port: 80}, "TCP", nil)
	select {
	case r := <-first:
		if r.UserHash != "hash" {
			t.Fatalf("收到别人的记录: %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("原订阅者在重复订阅被拒之后再没收到记录")
	}

	Unsubscribe(uid)
	if got := subscriberCount.Load(); got != base {
		t.Fatalf("退订之后计数 = %d，期望回到 %d", got, base)
	}
}
