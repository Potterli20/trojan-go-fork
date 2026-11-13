package redirector

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// 灌满会话上限后，第 N+1 条重定向必须被丢弃并关掉连接，而不是再开一个会话。
// 诱饵路径处理的是认证失败流量，发送方无需凭据；无上限时并发 = 到达速率 × 会话寿命。
func TestRedirectSessionCountIsBounded(t *testing.T) {
	maxRedirectSessions.Store(2)
	t.Cleanup(func() { maxRedirectSessions.Store(0) })

	r := NewRedirector(context.Background())
	t.Cleanup(func() { r.Close() })

	dialCount := atomic.Int32{}
	dialStarted := make(chan struct{}, 8)
	silentDial := func(net.Addr) (net.Conn, error) {
		dialCount.Add(1)
		dialStarted <- struct{}{}
		c, _ := net.Pipe() // 对端永不读写：会话靠关停收尾
		return c, nil
	}

	inbounds := make([]net.Conn, 0, 6)
	t.Cleanup(func() {
		for _, c := range inbounds {
			c.Close()
		}
	})
	for i := range 3 {
		c, peer := net.Pipe()
		inbounds = append(inbounds, c, peer)
		r.Redirect(&Redirection{Dial: silentDial, RedirectTo: &net.TCPAddr{}, InboundConn: c})
		if i < 2 {
			select {
			case <-dialStarted:
			case <-time.After(2 * time.Second):
				t.Fatal("前两条会话未按预期建立")
			}
		}
	}
	// 第三条的连接必须被关闭（丢弃），且 Dial 只被调了 2 次
	third := inbounds[4]
	third.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	if _, err := third.Read(buf); err == nil {
		t.Fatal("第三条连接未被丢弃：读到了数据")
	}
	if n := dialCount.Load(); n != 2 {
		t.Fatalf("Dial 被调 %d 次， want 2（上限外不应再建会话）", n)
	}
}
