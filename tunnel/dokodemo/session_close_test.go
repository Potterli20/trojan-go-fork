package dokodemo

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// TestClosedSessionIsReplacedForSameSource 钉住一条真实的黑洞：
// 会话 goroutine 的 select 只有 s.ctx.Done() 和空闲 timer，**不看 conn.ctx.Done()**，
// 而下游 relay 拆会话时调的正是 conn.Close()（只 cancel 了 conn.ctx）。
// 于是 mapping 里的死表项要一直挂满 UDPTimeout 才被 timer 分支清掉，
// 在此期间同源地址（ip:port 相同）的每个包都被 select/default 投进没人读的 input
// 直接丢掉，而且因为 found==true 永远不会再投递新会话 —— 客户端换个会话继续发 UDP
// 就彻底没反应。socks 的同型 daemon 早就监听 conn.ctx.Done()，这里属于漏改。
//
// timeout 故意设成 5 分钟，让 HEAD 上的"自愈"远在水准线之外，测的就是不能自愈的窗口。
func TestClosedSessionIsReplacedForSameSource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 41234}
	udp := &blockedUDP{
		feed:   make(chan []byte, 8),
		src:    src,
		gate:   make(chan struct{}),
		closed: make(chan struct{}),
	}
	defer close(udp.closed)

	s := &Server{
		udpListener: udp,
		packetChan:  make(chan tunnel.PacketConn, 8),
		timeout:     5 * time.Minute,
		targetAddr:  &tunnel.Address{AddressType: tunnel.IPv4, Port: 53},
		mapping:     map[string]*PacketConn{},
		ctx:         ctx,
		cancel:      cancel,
	}
	go s.dispatchLoop()

	udp.feed <- []byte("first")
	pc, err := s.AcceptPacket(&Tunnel{})
	if err != nil {
		t.Fatalf("AcceptPacket: %v", err)
	}
	buf := make([]byte, 32)
	n, _, err := pc.ReadWithMetadata(buf)
	if err != nil || string(buf[:n]) != "first" {
		t.Fatalf("首包没能送达会话: n=%d err=%v", n, err)
	}

	// 模拟 relay 拆掉这一侧的会话：只关 PacketConn，不关 Server
	if err := pc.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}

	// 同一源地址继续发包。found 为真时 dispatchLoop 会吞掉这一包，所以要持续重发，
	// 直到表项被回收、下一次投递出新会话为止。
	deadline := time.Now().Add(2 * time.Second)
	var fresh tunnel.PacketConn
	for time.Now().Before(deadline) {
		select {
		case udp.feed <- []byte("again"):
		case <-time.After(20 * time.Millisecond):
			continue
		}
		select {
		case fresh = <-s.packetChan:
		case <-time.After(100 * time.Millisecond):
			continue
		}
		if fresh != nil {
			break
		}
	}
	if fresh == nil {
		t.Fatal("会话关闭后同源地址再收不到新会话：mapping 里的死表项要等 UDPTimeout 才消失，" +
			"期间的 UDP 包全部黑洞（会话 goroutine 没监听 conn.ctx.Done()）")
	}

	// 表项必须已经换成新会话，而不是那个已关闭的旧对象
	s.mappingLock.Lock()
	current := s.mapping[src.String()]
	s.mappingLock.Unlock()
	if current != nil && current == fresh {
		return
	}
	t.Fatalf("mapping 表项没有指向刚投递的新会话：current=%v fresh=%v", current, fresh)
}
