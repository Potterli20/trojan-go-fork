package tls

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Potterli20/trojan-go-fork/redirector"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type countingServerUnderlay struct{ closes atomic.Int64 }

func (u *countingServerUnderlay) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	return nil, errors.New("not used")
}
func (u *countingServerUnderlay) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, errors.New("not used")
}
func (u *countingServerUnderlay) Close() error {
	u.closes.Add(1)
	return nil
}

// TestConcurrentServerCloseIsIdempotent 钉住 TLS Server.Close 的并发安全：
// proxy/stack.go 明确"重复关闭由各 tunnel 的幂等 Close 兜住"，共享下层会被
// FindAllEndpoints 重复命中。Server.Close 原先无幂等闸，并发/重复调用会：
// ① 重复关 underlay；② closePending 重复快照并关同一批握手连接；
// ③ 重复关 keyLogger（真实为 *os.File，第二次报 file already closed）；
// ④ 两个并发 wg.Wait 还可能与 acceptLoop 的 wg.Go Add 撞成非法 WaitGroup 用法。
// 变异对照：去掉 closeOnce，本用例在 -race 下报 data race，且 closes 变 N。
func TestConcurrentServerCloseIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	underlay := &countingServerUnderlay{}
	kl := &countingKeyLogger{}
	s := &Server{
		ctx:          ctx,
		cancel:       cancel,
		underlay:     underlay,
		keyLogger:    kl,
		redir:        redirector.NewRedirector(ctx),
		connChan:     make(chan tunnel.Conn, 4),
		wsChan:       make(chan tunnel.Conn, 4),
		pendingConns: make(map[tunnel.Conn]struct{}),
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
	if got := kl.closes.Load(); got != 1 {
		t.Fatalf("keyLogger.Close 被调用 %d 次，幂等 Close 应为 1 次", got)
	}
}
