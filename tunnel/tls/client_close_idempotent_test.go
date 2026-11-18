package tls

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type countingKeyLogger struct{ closes atomic.Int64 }

func (k *countingKeyLogger) Write(p []byte) (int, error) { return len(p), nil }
func (k *countingKeyLogger) Close() error {
	k.closes.Add(1)
	return nil
}

type countingTLSUnderlay struct{ closes atomic.Int64 }

func (u *countingTLSUnderlay) DialConn(*tunnel.Address, tunnel.Tunnel) (tunnel.Conn, error) {
	return nil, errors.New("not used")
}
func (u *countingTLSUnderlay) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, errors.New("not used")
}
func (u *countingTLSUnderlay) Close() error {
	u.closes.Add(1)
	return nil
}

// TestConcurrentClientCloseIsIdempotent 钉住 TLS Client.Close 的并发安全：
// 库化调用方（反复建栈/热重载）对同一 Client 并发 Close 时，旧实现每个 Close
// 都关一次 keyLogger 和 underlay。真实 keyLogger 是 *os.File，第二次 Close
// 报 "file already closed"；underlay 也被打多次（并级联放大到更下层）。
// proxy/stack.go:147 注释明确"重复关闭由各 tunnel 的幂等 Close 兜住"，
// TLS Client 侧无闸。
//
// 变异对照：去掉 closeOnce 幂等闸，本用例在 -race 下立刻报 data race，
// 且 closes 计数变成 N。
func TestConcurrentClientCloseIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	kl := &countingKeyLogger{}
	underlay := &countingTLSUnderlay{}
	c := &Client{ctx: ctx, keyLogger: kl, underlay: underlay}

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
	if got := kl.closes.Load(); got != 1 {
		t.Fatalf("keyLogger.Close 被调用 %d 次，幂等 Close 应为 1 次", got)
	}
	if got := underlay.closes.Load(); got != 1 {
		t.Fatalf("underlay.Close 被调用 %d 次，幂等 Close 应为 1 次", got)
	}
}
