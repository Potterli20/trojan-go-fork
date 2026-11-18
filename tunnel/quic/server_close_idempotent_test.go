package quic

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type countingQUICUnderlay struct{ closes atomic.Int64 }

func (u *countingQUICUnderlay) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	return nil, errors.New("not used")
}
func (u *countingQUICUnderlay) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, errors.New("not used")
}
func (u *countingQUICUnderlay) Close() error {
	u.closes.Add(1)
	return nil
}

// TestConcurrentServerCloseIsIdempotent 钉住 QUIC Server.Close 的并发安全：
// proxy/stack.go 明确"重复关闭由各 tunnel 的幂等 Close 兜住"，且注释点名
// quic 应已处理幂等——但 Close 全函数无闸，并发调用会重复关 listener /
// packetConn / activeConns / underlay，与注释直接矛盾。
//
// 用计数型 underlay 作可观测点：幂等 Close 下 underlay.Close 只应被调用 1 次。
// 变异对照：去掉 closeOnce，本用例 underlay.Close 被调用 N 次。
func TestConcurrentServerCloseIsIdempotent(t *testing.T) {
	certPath, keyPath := writeSelfSigned(t, t.TempDir())

	underlay := &countingQUICUnderlay{}
	port := common.PickPort("udp", "127.0.0.1")
	server, err := NewServer(quicServerCtx(t, port, certPath, keyPath), underlay)
	if err != nil {
		t.Fatalf("NewServer 失败：%v", err)
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
			errs[i] = server.Close()
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
