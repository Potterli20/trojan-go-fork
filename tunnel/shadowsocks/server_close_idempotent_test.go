package shadowsocks

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Potterli20/trojan-go-fork/redirector"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type countingSSUnderlay struct{ closes atomic.Int64 }

func (u *countingSSUnderlay) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	return nil, errors.New("not used")
}
func (u *countingSSUnderlay) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, errors.New("not used")
}
func (u *countingSSUnderlay) Close() error { u.closes.Add(1); return nil }

// TestConcurrentServerCloseIsIdempotent 钉住 shadowsocks.Server.Close 的并发安全：
// 与 websocket/tls.Server 同型（cancel → underlay.Close → closePending → redir.Close）。
// proxy/stack.go:147 明确共享下层会被重复命中、依赖各 tunnel 的幂等 Close。
// 无闸时并发 Close 会重复关 underlay、重复快照关在途探测连接、重复关嵌入的
// Redirector。变异对照：去掉 closeOnce，underlay 关闭计数变 N。
func TestConcurrentServerCloseIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	underlay := &countingSSUnderlay{}
	s := &Server{
		ctx:          ctx,
		cancel:       cancel,
		underlay:     underlay,
		Redirector:   redirector.NewRedirector(ctx),
		pendingConns: make(map[tunnel.Conn]struct{}),
	}

	const n = 8
	var start sync.WaitGroup
	start.Add(1)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start.Wait()
			errs[i] = s.Close()
		}(i)
	}
	start.Done()
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("第 %d 次 Close 返回错误：%v", i, err)
		}
	}
	if got := underlay.closes.Load(); got != 1 {
		t.Fatalf("underlay.Close 被调用 %d 次，幂等 Close 应为 1 次", got)
	}
}
