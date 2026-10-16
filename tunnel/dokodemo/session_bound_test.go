package dokodemo

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// multiSrcUDP 是可控的 net.PacketConn：ReadFrom 从 feed 取"外网来的包"，每个包
// 自带源地址。udp_session_timer_test.go 里的 blockedUDP 只有一个固定 src，测会话数
// 上限需要每个包来自不同源，所以另写一个。
type multiSrcUDP struct {
	feed   chan udpPacket
	writes counters
	closed chan struct{}
	once   sync.Once
}

type udpPacket struct {
	payload []byte
	src     net.Addr
}

func (u *multiSrcUDP) ReadFrom(p []byte) (int, net.Addr, error) {
	select {
	case pkt := <-u.feed:
		return copy(p, pkt.payload), pkt.src, nil
	case <-u.closed:
		return 0, nil, net.ErrClosed
	}
}

func (u *multiSrcUDP) WriteTo(p []byte, _ net.Addr) (int, error) {
	u.writes.bump()
	return len(p), nil
}

// counters 是带锁计数器，只给判据用。
type counters struct {
	mu sync.Mutex
	n  int
}

func (c *counters) bump() { c.mu.Lock(); c.n++; c.mu.Unlock() }
func (c *counters) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}
func (u *multiSrcUDP) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (u *multiSrcUDP) RemoteAddr() net.Addr             { return &net.UDPAddr{} }
func (u *multiSrcUDP) SetDeadline(time.Time) error      { return nil }
func (u *multiSrcUDP) SetReadDeadline(time.Time) error  { return nil }
func (u *multiSrcUDP) SetWriteDeadline(time.Time) error { return nil }

func (u *multiSrcUDP) Close() error {
	u.once.Do(func() { close(u.closed) })
	return nil
}

// TestUDPSessionCountIsBounded 钉住会话表上限。dokodemo 的 mapping 原本只有空闲
// 超时，条目数等于 UDPTimeout × 新源地址到达速率，而源地址可以伪造；每条会话攥着
// 两条深度 16 的通道、一个子 ctx 和一个守护协程。闸必须卡在造对象之前。
// 口径同 tunnel/tproxy/session_bound_test.go 与 tunnel/socks/session_bound_test.go。
func TestUDPSessionCountIsBounded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	udp := &multiSrcUDP{feed: make(chan udpPacket, 4), closed: make(chan struct{})}
	s := &Server{
		udpListener: udp,
		packetChan:  make(chan tunnel.PacketConn, 4),
		timeout:     5 * time.Minute,
		targetAddr: &tunnel.Address{
			AddressType: tunnel.IPv4,
			IP:          []byte{127, 0, 0, 1},
			Port:        8080,
		},
		mapping: make(map[string]*PacketConn, maxUDPSessions+1),
		ctx:     ctx,
		cancel:  cancel,
	}

	var first *PacketConn
	var overCap *net.UDPAddr
	for i := 0; i <= maxUDPSessions; i++ {
		src := &net.UDPAddr{IP: net.IPv4(10, 0, byte(i>>16), byte(i>>8)), Port: 3000 + i}
		if i < maxUDPSessions {
			pc := &PacketConn{
				input:  make(chan []byte, 16),
				output: make(chan []byte, 16),
				ctx:    ctx,
				cancel: func() {},
				src:    src,
			}
			s.mapping[src.String()] = pc
			if i == 0 {
				first = pc
			}
		} else {
			overCap = src
		}
	}

	go s.dispatchLoop()

	udp.feed <- udpPacket{payload: []byte("x"), src: overCap}
	// 同步点：投给一条**已存在**的会话。dispatch 是单消费者，这条进了 input 就说明
	// 上一条已经判完；表满时新源地址一律开不出会话，不能拿"新建一条"当同步点。
	udp.feed <- udpPacket{payload: []byte("y"), src: first.src}
	select {
	case payload := <-first.input:
		if len(payload) != 1 {
			t.Fatalf("同步点载荷不对: %q", payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch 循环没有处理投递的包，判据失效")
	}

	s.mappingLock.Lock()
	_, hasOverCap := s.mapping[overCap.String()]
	size := len(s.mapping)
	s.mappingLock.Unlock()

	if hasOverCap {
		t.Fatalf("会话表已有 %d 条时仍为新源地址开了会话：条目数由包量决定，没有上限", maxUDPSessions)
	}
	if size != maxUDPSessions {
		t.Fatalf("会话表 %d 条，上限是 %d", size, maxUDPSessions)
	}
}
