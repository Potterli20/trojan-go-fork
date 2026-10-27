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

// startDispatch 启动 dispatch 主循环并返回"已退出"信号。收尾必须走 stopDispatch：
// 主循环的读泵是 s.wg 成员，只 cancel 不会把它从 ReadFromUDP 上叫醒，而残留的
// dispatch/读泵可能在下一条用例中途归还手里的池 buffer，把包级借出计数搅成 -1
// （-race 全仓跑时实测过这种跨用例干扰）。
func startDispatch(s *Server) chan struct{} {
	done := make(chan struct{})
	go func() {
		s.packetDispatchLoop()
		close(done)
	}()
	return done
}

// stopDispatch 取消 ctx、关掉 UDP 监听叫醒读泵，并等到主循环与读泵都退出。
func stopDispatch(t *testing.T, s *Server, done chan struct{}) {
	t.Helper()
	s.cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel 之后 dispatch 没退出")
	}
	s.udpListener.Close() //gosec:disable -- 测试清理：叫醒阻塞在 ReadFromUDP 的读泵
	s.wg.Wait()
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
		mapping:     make(map[tunnel.SessionKey]*PacketConn),
		ctx:         ctx,
		cancel:      cancel,
	}
	dispatchDone := startDispatch(s)

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
	// 等 dispatch 主循环也退出，避免它在下一条用例中途归还 buffer 搅乱全局计数
	stopDispatch(t, s, dispatchDone)
}

// TestPooledPayloadsAreReturnedOnLimitDrops 钉住**会话上限拒绝**这条丢包出口的账目。
//
// AGENTS.md 写的口径是"归还点必须覆盖所有丢包出口（投递 default:、地址不可表示、
// 会话上限拒绝）"。dokodemo 与 socks 都照做了，tproxy 是唯一的违约方：闸门命中时
// info 已经从 packetQueue 取走、手里的 8KB 是从池里借的，而那条 continue 直接把它
// 扔在原地——既不归池，也不在任何队列里（所以 Close 的排空够不着）。
//
// 后果不是"少复用一点"而是**每包**永久离开池：闸门后面的丢弃速率等于新源地址到达
// 速率，而源地址可伪造，于是池被抽干，之后每个包都重新 make 8KB，分配次数回到与包数
// 同阶——正是这一轮池化要消灭的东西。
//
// 判据用**同步点**收口：灌满会话表之后投 N 个新源地址的包，再投一个给已存在的会话，
// 那个包出现在会话 input 里就说明前面 N 条都已经判完，此时借出计数必须回到起点。
// 上限分支的 payload 用池外的一次性切片，避免给账目掺进同步点自己那一条。
func TestPooledPayloadsAreReturnedOnLimitDrops(t *testing.T) {
	base := borrowedPacketBufs()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	udpListener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer udpListener.Close() //gosec:disable -- 错误忽略：测试清理

	s := &Server{
		udpListener: udpListener,
		packetQueue: make(chan *tproxyPacketInfo, 4),
		packetChan:  make(chan tunnel.PacketConn, 2),
		timeout:     5 * time.Minute,
		mapping:     make(map[tunnel.SessionKey]*PacketConn, maxUDPSessions+1),
		ctx:         ctx,
		cancel:      cancel,
	}
	// 把表灌满：会话对象不必活着，这里要测的是建表前那道闸之后的归还路径
	srcs := make([]*net.UDPAddr, 0, maxUDPSessions+1)
	for i := range maxUDPSessions + 1 {
		src := &net.UDPAddr{IP: net.IPv4(10, 0, 0, byte(i>>8)), Port: 1000 + i}
		srcs = append(srcs, src)
		if i < maxUDPSessions {
			s.mapping[tunnel.NewSessionKey(src)] = &PacketConn{
				input:  make(chan *packetInfo, udpSessionQueueSize),
				output: make(chan *packetInfo, udpSessionQueueSize),
				ctx:    ctx,
				cancel: func() {},
				src:    src,
			}
		}
	}
	dispatchDone := startDispatch(s)

	const drops = 32
	for i := range drops {
		src := &net.UDPAddr{IP: net.IPv4(192, 0, 2, byte(i>>8)), Port: 20000 + i}
		buf := getPacketBuf()
		n := copy(buf, []byte("dropped at the gate"))
		s.packetQueue <- &tproxyPacketInfo{src: src, dst: src, payload: buf[:n]}
	}

	s.packetQueue <- &tproxyPacketInfo{src: srcs[0], dst: srcs[0], payload: []byte("y")}
	select {
	case info := <-sessionQueueOf(s, srcs[0]):
		if len(info.payload) != 1 {
			t.Fatalf("同步点收到的载荷不对：%q", info.payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch 循环没有处理投递的包，判据失效")
	}

	if !waitOutstanding(base) {
		t.Fatalf("%d 个在会话上限处被丢弃的包里，有 %d 块池 buffer 没归还："+
			"源地址可伪造，到上限之后每丢一包就永久离开池一块 8KB，池会被彻底抽干",
			drops, borrowedPacketBufs()-base)
	}
	// 等 dispatch 与读泵退出，避免残留协程在后续用例里归还 buffer 搅乱全局计数
	stopDispatch(t, s, dispatchDone)
}

// TestPooledPayloadReturnedWhenDownstreamGone 覆盖另一半关停出口：会话建好了，但
// `s.packetChan <- conn` 撞上 ctx.Done 而 return。这条 info 同样已经离开 packetQueue，
// Close 的排空只覆盖队列里没取走的，所以这一块必须自己还（每次关停漏一块）。
//
// 时序做成确定性的：packetChan 用**无缓冲**通道，dispatch 必然卡在这次发送上；测试
// 轮询到 mapping 里出现那条会话才 cancel，从而保证被判的是发送分支而不是队列读分支
// （后者在 ctx 已取消时与取包是随机 select，拿它当判据会偶发空转）。
func TestPooledPayloadReturnedWhenDownstreamGone(t *testing.T) {
	base := borrowedPacketBufs()
	ctx, cancel := context.WithCancel(context.Background())
	udpListener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer udpListener.Close() //gosec:disable -- 错误忽略：测试清理

	s := &Server{
		udpListener: udpListener,
		packetQueue: make(chan *tproxyPacketInfo, 4),
		packetChan:  make(chan tunnel.PacketConn), // 无缓冲：没人接，发送必然阻塞
		timeout:     5 * time.Minute,
		mapping:     make(map[tunnel.SessionKey]*PacketConn),
		ctx:         ctx,
		cancel:      cancel,
	}
	dispatchDone := startDispatch(s)

	src := &net.UDPAddr{IP: net.IPv4(198, 51, 100, 7), Port: 33333}
	buf := getPacketBuf()
	n := copy(buf, []byte("session with nowhere to go"))
	s.packetQueue <- &tproxyPacketInfo{src: src, dst: src, payload: buf[:n]}

	// 等 dispatch 建好会话并卡在 packetChan 发送上
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.mappingLock.RLock()
		ready := len(s.mapping) == 1
		s.mappingLock.RUnlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("dispatch 没有建立会话，用例没走到 packetChan 那一跳，判据失效")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := borrowedPacketBufs() - base; got != 1 {
		t.Fatalf("会话建好、正卡在投递给上游的那一刻，借出计数是 %d，期望 1（这条包还捏在 dispatch 手里）", got)
	}

	cancel()
	select {
	case <-dispatchDone:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel 之后 dispatch 没退出")
	}
	if !waitOutstanding(base) {
		got := borrowedPacketBufs() - base
		if got < 0 {
			t.Fatalf("关停后借出计数比起点还低 %d 块：这是**双归还**，比漏还严重——两个包会共享同一块"+
				"底层数组互相串数据。检查这个出口是否既调了 conn.Close()（它排空 input 并归还）又自己 put 了一次", -got)
		}
		t.Fatalf("关停时手里那条包的 8KB 没归还（还差 %d 块）：它已经离开 packetQueue，"+
			"Close 的排空够不着它", got)
	}
	// 守护协程从没起来，表项只能由这个出口删；否则 mapping 永久挂着一条已 Close 的会话
	s.mappingLock.RLock()
	left := len(s.mapping)
	s.mappingLock.RUnlock()
	if left != 0 {
		t.Fatalf("关停后 mapping 里还剩 %d 条死会话：守护协程没起来，表项无人删除", left)
	}
	// 读泵是 wg 成员，关监听后等它退出，别把收尾留到下一条用例
	s.udpListener.Close() //gosec:disable -- 测试清理：叫醒阻塞在 ReadFromUDP 的读泵
	s.wg.Wait()
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
