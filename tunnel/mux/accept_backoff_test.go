package mux

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/xtaci/smux"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// failingMuxUnderlay 的 AcceptConn 一律报错，但 ctx 取消前不返回 net.ErrClosed——
// 复现"监听还活着却持续吐错误"的形态。limit/limitMu/onLimit 是同步自限位：
// 正确实现跑不到这个数；退避被改回裸 continue 时循环永不阻塞、虚拟时间无法前进，
// 靠这道闸 cancel 才能收尾并报出确切次数（形态同 tunnel/adapter 的同名判据）。
type failingMuxUnderlay struct {
	calls   atomic.Int64
	limit   int64
	limitMu sync.Mutex
	onLimit func()
}

func (u *failingMuxUnderlay) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	if n := u.calls.Add(1); u.limit > 0 && n >= u.limit {
		u.limitMu.Lock()
		fn := u.onLimit
		u.onLimit = nil
		u.limitMu.Unlock()
		if fn != nil {
			fn()
		}
	}
	return nil, errors.New("accept tcp: too many open files")
}
func (u *failingMuxUnderlay) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, errors.New("not used")
}
func (u *failingMuxUnderlay) Close() error { return nil }
func (u *failingMuxUnderlay) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
}

// TestPersistentAcceptErrorBacksOff 钉住 mux.Server.acceptConnWorker 的错误分支：
// 旧的写法是 "ctx 没取消就裸 continue"，下层持续报错（fd 耗尽、共享监听被外部
// 关掉而本层 ctx 未取消）就是一条满 CPU 的热循环。同型缺陷在 adapter/trojan/
// simplesocks/socks/tproxy 都按 100ms 退避修过，这里是漏网的姊妹路径。
//
// 判据跑在 testing/synctest 气泡里，虚拟时间只在所有协程 durably blocked 时前进，
// "500ms 里退避了几次"是精确读数。把退避改回裸 continue，计数在虚拟时间里冲到
// limit 被 onLimit cancel，判据当场变红。
func TestPersistentAcceptErrorBacksOff(t *testing.T) {
	u := &failingMuxUnderlay{limit: 200}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Server{
		underlay: u,
		ctx:      ctx,
		cancel:   cancel,
		connChan: make(chan tunnel.Conn, 4),
		sessions: make(map[*smux.Session]struct{}),
	}
	u.onLimit = cancel

	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		go func() {
			s.acceptConnWorker()
			close(done)
		}()

		time.Sleep(500 * time.Millisecond)
		calls := u.calls.Load()
		if calls == 0 {
			t.Fatal("用例没进入 Accept 的错误分支，判据失效")
		}
		// 100ms 退避 × 500ms 虚拟时间 = 至多 6 次。
		if calls > 6 {
			t.Fatalf("500ms 虚拟时间里 Accept 被调用了 %d 次：持续报错没有退避，这是一条满 CPU 的热循环", calls)
		}

		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("ctx 取消后 acceptConnWorker 没有退出：退避必须同时听 ctx.Done")
		}
	})
}
