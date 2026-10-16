//go:build linux

package tproxy

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// waitOutstanding 有界等待借出计数落到 want，返回是否等到。
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
// 借用权的路径是：读协程借 → packetQueue → dispatch → 会话 input → ReadWithMetadata
// 归还；反向是 WriteWithMetadata 借 → 会话 output → 守护协程写出去后归还。这条链上任何
// 一个出口漏了归还，池就被抽空一截——堆内存能被 GC 回收，但之后每包都重新 make 8KB，
// 分配次数回到与包数同阶，池化等于白做；而**双归还**比漏归还严重得多：两个包会共享
// 同一块底层数组，互相把对方的负载串掉，且只在真实流量下偶发。
//
// 计数是包级的（Get 加、Put 减），所以判据是确定性的：一轮突发 + 拆会话 + 关服务端
// 之后必须归零。为负即双归还，为正即漏归还。
func TestPacketBufsAreReturnedOnBothDirections(t *testing.T) {
	base := borrowedPacketBufs()
	if base != 0 {
		// 不是错误：conn_test.go 里有一条只验"写方向确实复制"的用例，它建了会话、
		// 写了一个包就结束，从不 Close，于是那 1 块借出的负载没人归还（测试夹具的
		// 形状问题，生产路径上会话一律由守护协程的 defer 或 relay 关掉）。这里按
		// **增量**判：起点是多少不重要，一轮突发 + 拆会话 + 关服务端之后必须回到起点。
		t.Logf("用例开始时借出计数为 %d（conn_test.go 的夹具留的），按增量判", base)
	}

	origDial := dialUDP
	t.Cleanup(func() { dialUDP = origDial })

	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("peer: %v", err)
	}
	defer peer.Close() //gosec:disable -- 错误忽略：测试清理
	src := peer.LocalAddr().(*net.UDPAddr)
	dialUDP = func(_ string, _ *net.UDPAddr, raddr *net.UDPAddr) (*net.UDPConn, error) {
		return net.DialUDP("udp", nil, raddr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	udpListener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer udpListener.Close() //gosec:disable -- 错误忽略：测试清理

	// Close 会走 closeListeners，那里面无条件关 tcpListener；夹具必须真给一个，
	// 否则排空 packetQueue 那段根本执行不到
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer tcpListener.Close() //gosec:disable -- 错误忽略：测试清理

	s := &Server{
		tcpListener: tcpListener,
		udpListener: udpListener,
		packetQueue: make(chan *tproxyPacketInfo, 16),
		packetChan:  make(chan tunnel.PacketConn, 8),
		timeout:     5 * time.Minute,
		mapping:     make(map[string]*PacketConn),
		ctx:         ctx,
		cancel:      cancel,
	}
	go s.packetDispatchLoop()

	// 入向：照读协程的形状借一块池 buffer 投进队列，dispatch 负责转交给会话
	const inbound = 12
	for range inbound {
		buf := getPacketBuf()
		n := copy(buf, []byte("inbound datagram"))
		s.packetQueue <- &tproxyPacketInfo{
			src:     src,
			dst:     src,
			payload: buf[:n],
		}
	}
	pc, err := s.AcceptPacket(&Tunnel{})
	if err != nil {
		t.Fatalf("AcceptPacket: %v", err)
	}
	session := pc.(*PacketConn)

	recv := make([]byte, MaxPacketSize)
	for i := range inbound {
		n, _, err := session.ReadWithMetadata(recv)
		if err != nil {
			t.Fatalf("第 %d 个入向包: %v", i, err)
		}
		if n == 0 {
			t.Fatalf("第 %d 个入向包长度为 0", i)
		}
	}

	// 出向：WriteWithMetadata 自己借，守护协程写出去后归还
	const outbound = 7
	meta := fakeBindAddress(23000)
	for range outbound {
		if _, err := session.WriteWithMetadata([]byte("outbound datagram"), meta); err != nil {
			t.Fatalf("出向包写入失败: %v", err)
		}
	}
	if n := countPackets(t, peer, outbound); n != outbound {
		t.Fatalf("客户端只收到 %d 个回包，期望 %d", n, outbound)
	}

	// 拆会话 + 关服务端：Close 要排空 input/output 与 packetQueue 并逐个归还
	if err := session.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}
	s.Close() //gosec:disable -- 错误忽略：测试清理

	if !waitOutstanding(base) {
		got := borrowedPacketBufs()
		switch {
		case got > base:
			t.Fatalf("一轮 %d 入 + %d 出之后仍有 %d 块池 buffer 没归还：池会被一点点抽空，"+
				"之后每包重新 make 8KB", inbound, outbound, got-base)
		default:
			t.Fatalf("借出计数变成 %d（起始 %d）：有 buffer 被归还了两次，"+
				"两个包会共享同一块底层数组、互相串数据", got, base)
		}
	}
}

// TestOversizedPayloadIsNotTruncated 钉住"超尺寸负载走一次性分配"的兜底：池里的对象
// 只有 MaxPacketSize，直接 copy 进去会截断，而 putPacketBuf 的 cap 闸门保证这类
// 一次性分配不会被塞回池里把水位顶高。
func TestOversizedPayloadIsNotTruncated(t *testing.T) {
	base := borrowedPacketBufs()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &PacketConn{
		input:  make(chan *packetInfo, 4),
		output: make(chan *packetInfo, 4),
		ctx:    ctx,
		cancel: cancel,
		src:    &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1},
	}
	payload := make([]byte, MaxPacketSize+1024)
	for i := range payload {
		payload[i] = byte(i)
	}
	n, err := c.WriteWithMetadata(payload, &tunnel.Metadata{
		Command: tunnel.Command(3),
		Address: &tunnel.Address{AddressType: tunnel.IPv4, IP: []byte{1, 2, 3, 4}, Port: 53},
	})
	if err != nil {
		t.Fatalf("WriteWithMetadata: %v", err)
	}
	if n != len(payload) {
		t.Fatalf("写入长度 %d，期望 %d：超尺寸负载被截断了", n, len(payload))
	}
	info := <-c.output
	if len(info.payload) != len(payload) {
		t.Fatalf("入队长度 %d，期望 %d", len(info.payload), len(payload))
	}
	putPacketBuf(info.payload)
	if got := borrowedPacketBufs(); got != base {
		t.Fatalf("超尺寸负载的一次性分配被算进了池账目（%d vs %d）", got, base)
	}
}

// BenchmarkWriteWithMetadata 给"每包一次 8KB 分配"换成池化前后一个可比数字。
// 跑法：go test -run '^$' -bench BenchmarkWriteWithMetadata -benchmem ./tunnel/tproxy/
func BenchmarkWriteWithMetadata(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &PacketConn{
		input:  make(chan *packetInfo, 1024),
		output: make(chan *packetInfo, 1024),
		ctx:    ctx,
		cancel: cancel,
		src:    &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1},
	}
	meta := &tunnel.Metadata{
		Command: tunnel.Command(3),
		Address: &tunnel.Address{AddressType: tunnel.IPv4, IP: []byte{1, 2, 3, 4}, Port: 53},
	}
	payload := make([]byte, 1400)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := c.WriteWithMetadata(payload, meta); err != nil {
			b.Fatal(err)
		}
		// 消费端归还：这就是真实链路上的所有权流转，不归还的话基准测的是"只借不还"
		info := <-c.output
		putPacketBuf(info.payload)
	}
}
