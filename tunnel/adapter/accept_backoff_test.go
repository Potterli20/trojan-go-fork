package adapter

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// failingListener 的 Accept 一律报错，但 Close 之前它不会返回 net.ErrClosed——
// 复现的是"监听还活着、却持续吐错误"的形态（fd 耗尽是最常见的一种）。
type failingListener struct {
	calls   atomic.Int64
	closed  chan struct{}
	once    sync.Once
	limit   int64
	limitMu sync.Mutex
	onLimit func()
}

// limit 到量后调用 onLimit（测试里是 cancel）：正确实现根本到不了这个数，
// 而退避被改回裸 continue 时循环会一直自转、假时钟永不前进——靠这道闸它才能在
// 有限时间内收尾，判据于是能报出确切次数而不是一路挂到 test timed out。
func (l *failingListener) Accept() (net.Conn, error) {
	if n := l.calls.Add(1); l.limit > 0 && n >= l.limit {
		l.limitMu.Lock()
		fn := l.onLimit
		l.onLimit = nil
		l.limitMu.Unlock()
		if fn != nil {
			fn()
		}
	}
	select {
	case <-l.closed:
		return nil, net.ErrClosed
	default:
	}
	return nil, errors.New("accept tcp 127.0.0.1:0: too many open files")
}

func (l *failingListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *failingListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1} }

// TestPersistentAcceptErrorDoesNotSpin 盯住 acceptConnLoop 的错误分支：旧的写法是
// "ctx 没取消就裸 continue"，于是监听一旦**持续**报错（fd 耗尽、监听被外部关掉而 ctx
// 未取消）就是一条满 CPU 的热循环，而且一行日志都没有——比直接崩掉更难发现。
// 同型的 UDP 读循环（socks 实测 400ms 内读了 3500 万次）早就改成 100ms 退避 + 每 10 次
// 一条 Error，TCP 这一条是唯一还没补的姊妹路径。
//
// 判据跑在 Go 1.27 的 testing/synctest 气泡里：虚拟时间只在气泡内所有协程都 durably
// blocked 时才前进，所以"500ms 里退避了几次"是精确读数，不吃机器负载、不等真实墙钟。
// 把退避改回裸 continue，循环就永远不会阻塞，计数在虚拟时间里直接爆掉，判据当场变红。
func TestPersistentAcceptErrorDoesNotSpin(t *testing.T) {
	ln := &failingListener{closed: make(chan struct{}), limit: 200}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Server{
		tcpListener: ln,
		socksConn:   make(chan tunnel.Conn, 4),
		httpConn:    make(chan tunnel.Conn, 4),
		ctx:         ctx,
		cancel:      cancel,
	}

	ln.onLimit = cancel
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		go func() {
			s.acceptConnLoop()
			close(done)
		}()

		time.Sleep(500 * time.Millisecond)
		calls := ln.calls.Load()
		if calls == 0 {
			t.Fatal("用例没进入 Accept 的错误分支，判据失效")
		}
		// 100ms 退避 × 500ms 虚拟时间 = 至多 6 次（首轮立即失败，其后每 100ms 一次）。
		if calls > 6 {
			t.Fatalf("500ms 虚拟时间里 Accept 被调用了 %d 次：持续报错没有退避，这是一条满 CPU 的热循环", calls)
		}

		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("ctx 取消后 acceptConnLoop 没有退出：退避必须同时听 ctx.Done")
		}
	})
}
