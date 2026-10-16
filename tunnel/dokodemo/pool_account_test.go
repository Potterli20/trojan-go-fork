package dokodemo

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// waitOutstanding 有界等待借出计数落到 want。
func waitOutstanding(want int64) bool {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if borrowedPacketBufs() == want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return borrowedPacketBufs() == want
}

func waitWrites(u *multiSrcUDP, want int) bool {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if u.writes.get() >= want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return u.writes.get() >= want
}

func newAccountingServer(ctx context.Context, cancel context.CancelFunc, udp *multiSrcUDP) *Server {
	return &Server{
		udpListener: udp,
		packetChan:  make(chan tunnel.PacketConn, 4),
		timeout:     5 * time.Minute,
		targetAddr: &tunnel.Address{
			AddressType: tunnel.IPv4,
			IP:          []byte{127, 0, 0, 1},
			Port:        8080,
		},
		mapping: make(map[string]*PacketConn),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// TestPacketBufsAreReturnedOnBothDirections 钉住池化负载的账目。
//
// 借用权的路径：dispatch 借 → 会话 input → ReadWithMetadata 归还；反向是
// WriteWithMetadata 借 → 会话 output → 守护协程 WriteTo 之后归还。漏归还的代价是池被
// 一点点抽空（之后每包重新 make 8KB，池化等于白做）；**双归还**更严重——两个包共享
// 同一块底层数组，互相把对方的负载串掉。计数是包级的，所以判据是确定性的。
// 同型判据见 tunnel/tproxy/pool_account_test.go、tunnel/socks/pool_account_test.go。
func TestPacketBufsAreReturnedOnBothDirections(t *testing.T) {
	base := borrowedPacketBufs()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	udp := &multiSrcUDP{feed: make(chan udpPacket, 32), closed: make(chan struct{})}
	s := newAccountingServer(ctx, cancel, udp)
	go s.dispatchLoop()

	src := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 51001}
	const (
		inbound  = 10
		outbound = 6
	)
	for range inbound {
		udp.feed <- udpPacket{payload: []byte("hello udp"), src: src}
	}

	select {
	case pc := <-s.packetChan:
		session := pc.(*PacketConn)
		buf := make([]byte, MaxPacketSize)
		for i := range inbound {
			n, _, err := session.ReadWithMetadata(buf)
			if err != nil {
				t.Fatalf("第 %d 个入向包: %v", i, err)
			}
			if n != len("hello udp") {
				t.Fatalf("第 %d 个入向包长度 %d，期望 %d", i, n, len("hello udp"))
			}
		}
		meta := &tunnel.Metadata{
			Command: tunnel.Command(3),
			Address: &tunnel.Address{AddressType: tunnel.IPv4, IP: []byte{1, 2, 3, 4}, Port: 53},
		}
		for range outbound {
			if _, err := session.WriteWithMetadata([]byte("reply"), meta); err != nil {
				t.Fatalf("出向包写入失败: %v", err)
			}
		}
		// 等守护协程真把它们写出去：否则这一轮测到的是 Close 的排空路径，
		// 守护协程那侧的归还根本没被走到
		if !waitWrites(udp, outbound) {
			t.Fatalf("守护协程只写出了 %d/%d 个包", udp.writes.get(), outbound)
		}
		if err := session.Close(); err != nil {
			t.Fatalf("close session: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch 没有把会话交给 packetChan，判据失效")
	}

	if !waitOutstanding(base) {
		got := borrowedPacketBufs()
		if got > base {
			t.Fatalf("一轮 %d 入 + %d 出之后仍有 %d 块池 buffer 没归还：池会被一点点抽空", inbound, outbound, got-base)
		}
		t.Fatalf("借出计数变成 %d（起始 %d）：有 buffer 被归还了两次，两个包会共享同一块底层数组", got, base)
	}
}

// TestQueueDropReturnsBuffer 钉住投递失败那条出口：input 满了就丢包，但丢包的同时必须
// 把借出的负载还回去——它不走消费端，也就不经过任何归还点，是池化之后最容易漏的一处。
func TestQueueDropReturnsBuffer(t *testing.T) {
	base := borrowedPacketBufs()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	udp := &multiSrcUDP{feed: make(chan udpPacket, 64), closed: make(chan struct{})}
	s := newAccountingServer(ctx, cancel, udp)
	go s.dispatchLoop()

	src := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 51002}
	const queueDepth = 16
	const total = queueDepth + 5
	for range total {
		udp.feed <- udpPacket{payload: []byte("drop me"), src: src}
	}

	// 会话建出来但**不读 input**：队列填满之后剩下的包必然走 default 分支被丢
	deadline := time.Now().Add(5 * time.Second)
	var session *PacketConn
	for time.Now().Before(deadline) {
		s.mappingLock.Lock()
		pc := s.mapping[src.String()]
		s.mappingLock.Unlock()
		if pc != nil && len(pc.input) == queueDepth {
			session = pc
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if session == nil {
		t.Fatal("会话没有建出来或队列没有填满，判据失效")
	}

	session.Close()

	if !waitOutstanding(base) {
		got := borrowedPacketBufs()
		if got > base {
			t.Fatalf("丢包路径漏归还 %d 块 buffer：投递失败是最容易漏的一处出口", got-base)
		}
		t.Fatalf("借出计数变成 %d（起始 %d）：有 buffer 被归还了两次", got, base)
	}
}
