package simplesocks

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type countingSimplesocksClientUnderlay struct{ closes atomic.Int64 }

func (u *countingSimplesocksClientUnderlay) DialConn(*tunnel.Address, tunnel.Tunnel) (tunnel.Conn, error) {
	return nil, errors.New("not used")
}
func (u *countingSimplesocksClientUnderlay) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, errors.New("not used")
}
func (u *countingSimplesocksClientUnderlay) Close() error { u.closes.Add(1); return nil }

// TestConcurrentClientCloseIsIdempotent 钉住 simplesocks.Client.Close 的并发安全：
// proxy/stack.go:147 明确共享下层会被重复命中、依赖各 tunnel 的幂等 Close。
// 本层 Close 只有一句 underlay.Close，无闸时并发调用会把同一下层关 N 次。
// 变异对照：去掉 closeOnce，本用例 underlay 关闭计数变 N。
func TestConcurrentClientCloseIsIdempotent(t *testing.T) {
	underlay := &countingSimplesocksClientUnderlay{}
	c := &Client{underlay: underlay}

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
			errs[i] = c.Close()
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
