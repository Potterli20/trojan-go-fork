package socks

import (
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

// TestPacketBufsAreReturnedOnBothDirections 钉住池化负载的账目。
//
// 借用权的路径：dispatch 从池里借 → 会话 input → ReadWithMetadata 归还；反向是
// WriteWithMetadata 借 → 会话 output → 守护协程拼帧写出去后归还。这条链上任何一个出口
// 漏了归还，池就被抽空一截（堆内存能被 GC 回收，但之后每包重新 make 8KB，池化等于白做）；
// 而**双归还**比漏归还严重得多：两个包共享同一块底层数组，互相把对方的负载串掉。
// 计数是包级的（Get 加、Put 减），所以判据是确定性的。
// 同型判据见 tunnel/tproxy/pool_account_test.go。
func TestPacketBufsAreReturnedOnBothDirections(t *testing.T) {
	base := borrowedPacketBufs()

	ctx := t.Context()
	conn := &multiSrcListenConn{feed: make(chan srcPacket, 32), closed: make(chan struct{})}
	s, _, _ := newSeededServer(ctx, conn, 0)
	go s.packetDispatchLoop()

	src := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 50001}
	const (
		inbound  = 10
		outbound = 6
	)
	for range inbound {
		conn.feed <- srcPacket{payload: socksPacket("hello udp"), src: src}
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
		if !waitWrites(conn, outbound) {
			t.Fatalf("守护协程只写出了 %d/%d 个包", conn.writes.get(), outbound)
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

// TestQueueDropReturnsBuffer 钉住投递失败那条出口：input 满了就丢包，但丢包的同时
// 必须把借出的负载还回去——旧写法这里连 buffer 都还没有池，换成池化之后这个 default
// 分支就是最容易漏的一处（它不走消费端，也就不经过任何归还点）。
func TestQueueDropReturnsBuffer(t *testing.T) {
	base := borrowedPacketBufs()

	ctx := t.Context()
	conn := &multiSrcListenConn{feed: make(chan srcPacket, 64), closed: make(chan struct{})}
	s, _, _ := newSeededServer(ctx, conn, 0)

	// 先把会话建出来，但**不**从 packetChan 取走，也不读 input：
	// 队列深度 udpSessionQueueSize，多投的包必然走 default 分支被丢
	src := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 50002}
	const total = udpSessionQueueSize + 5
	for range total {
		conn.feed <- srcPacket{payload: socksPacket("drop me"), src: src}
	}
	go s.packetDispatchLoop()

	// 等 dispatch 把这一批都处理完：用"表里出现该源地址 + 队列已满"作为判定点
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mappingLock.RLock()
		pc, ok := s.mapping[tunnel.NewSessionKey(src)]
		s.mappingLock.RUnlock()
		if ok && len(pc.input) == udpSessionQueueSize {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	s.mappingLock.RLock()
	pc := s.mapping[tunnel.NewSessionKey(src)]
	s.mappingLock.RUnlock()
	if pc == nil {
		t.Fatal("会话没有建出来，判据失效")
	}
	// 拆掉会话：队列里剩下的由 Close 的排空归还，被丢掉的应当在 default 分支就地归还
	pc.Close()

	if !waitOutstanding(base) {
		got := borrowedPacketBufs()
		if got > base {
			t.Fatalf("丢包路径漏归还 %d 块 buffer：投递失败是最容易漏的一处出口", got-base)
		}
		t.Fatalf("借出计数变成 %d（起始 %d）：有 buffer 被归还了两次", got, base)
	}
}

func waitWrites(conn *multiSrcListenConn, want int) bool {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if conn.writes.get() >= want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return conn.writes.get() >= want
}
