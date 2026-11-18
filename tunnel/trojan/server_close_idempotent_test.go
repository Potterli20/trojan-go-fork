package trojan

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Potterli20/trojan-go-fork/redirector"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type countingTrojanUnderlay struct{ closes atomic.Int64 }

func (u *countingTrojanUnderlay) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	return nil, errors.New("not used")
}
func (u *countingTrojanUnderlay) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, errors.New("not used")
}
func (u *countingTrojanUnderlay) Close() error { u.closes.Add(1); return nil }

// TestConcurrentServerCloseIsIdempotent 钉住 trojan.Server.Close 的并发安全：
// 它原有 closeOnce 只保护 releaseAuth（引用计数），整体 Close 无闸。并发 Close
// 会重复关 underlay、重复跑三个 channel 的排空、重复关 redir。proxy/stack.go:147
// 明确共享下层会被重复命中、依赖各 tunnel 的幂等 Close。
// 变异对照：去掉 closeAllOnce，underlay 关闭计数变 N。
func TestConcurrentServerCloseIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	underlay := &countingTrojanUnderlay{}
	s := &Server{
		underlay:   underlay,
		connChan:   make(chan tunnel.Conn, 4),
		muxChan:    make(chan tunnel.Conn, 4),
		packetChan: make(chan tunnel.PacketConn, 4),
		ctx:        ctx,
		cancel:     cancel,
		redir:      redirector.NewRedirector(ctx),
	}

	const n = 8
	var start sync.WaitGroup
	start.Add(1)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
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
