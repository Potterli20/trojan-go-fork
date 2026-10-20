package dokodemo

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// flakyUDP 让前 N 次 ReadFrom 返回错误，之后照常从 feed 取包。
type flakyUDP struct {
	errorsLeft atomic.Int32
	feed       chan udpPacket
	closed     chan struct{}
	once       sync.Once
}

func (u *flakyUDP) ReadFrom(p []byte) (int, net.Addr, error) {
	if u.errorsLeft.Add(-1) >= 0 {
		return 0, nil, errors.New("transient read error")
	}
	select {
	case pkt := <-u.feed:
		return copy(p, pkt.payload), pkt.src, nil
	case <-u.closed:
		return 0, nil, net.ErrClosed
	}
}

func (u *flakyUDP) WriteTo(p []byte, _ net.Addr) (int, error) { return len(p), nil }
func (u *flakyUDP) Close() error {
	u.once.Do(func() { close(u.closed) })
	return nil
}
func (u *flakyUDP) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (u *flakyUDP) SetDeadline(time.Time) error      { return nil }
func (u *flakyUDP) SetReadDeadline(time.Time) error  { return nil }
func (u *flakyUDP) SetWriteDeadline(time.Time) error { return nil }

// TestTransientReadErrorDoesNotKillUDPDispatch 钉住 UDP dispatch 的读错误出口：
// 旧实现遇到任何 ReadFrom 错误就 return，一次瞬时错误（部分平台会把 ICMP 相关
// 错误冒到 PacketConn 上）就会让 dokodemo 的 UDP 转发永久停摆，直到重启进程。
// socks/tproxy 的同型循环早就是"退避 + 重试"，这里对齐。
func TestTransientReadErrorDoesNotKillUDPDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	udp := &flakyUDP{feed: make(chan udpPacket, 4), closed: make(chan struct{})}
	udp.errorsLeft.Store(1)

	s := &Server{
		udpListener: udp,
		packetChan:  make(chan tunnel.PacketConn, 4),
		timeout:     5 * time.Minute,
		targetAddr:  &tunnel.Address{AddressType: tunnel.IPv4, Port: 53},
		mapping:     make(map[tunnel.SessionKey]*PacketConn),
		ctx:         ctx,
		cancel:      cancel,
	}
	done := make(chan struct{})
	go func() {
		s.dispatchLoop()
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		udp.Close() //gosec:disable -- 错误忽略：测试清理
		<-done
		s.wg.Wait()
	})

	udp.feed <- udpPacket{
		payload: []byte("after the error"),
		src:     &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 33333},
	}

	select {
	case pc := <-s.packetChan:
		pc.Close() //gosec:disable -- 错误忽略：测试清理
	case <-time.After(2 * time.Second):
		t.Fatal("一次瞬时读错误就让 dokodemo 的 UDP dispatch 永久停摆")
	}
}
