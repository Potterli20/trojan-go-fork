//go:build linux

package tproxy

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// TestReplySocketCacheIsBounded 盯的是"一条会话一个 fd 放大器"。
// 会话按源地址复用，目的地由对端决定；回包 socket 缓存若只 append 不删，
// 一条长期活跃的会话每遇到一个新目的地就永久多攥一个 socket，直到会话结束
// 才统一回收——而上限（空闲超时）是攻击者可以用流量维持的。
func TestReplySocketCacheIsBounded(t *testing.T) {
	origDial := dialUDP
	t.Cleanup(func() { dialUDP = origDial })

	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("peer: %v", err)
	}
	defer peer.Close() //gosec:disable -- 错误忽略：测试清理
	src := peer.LocalAddr().(*net.UDPAddr)

	var mu sync.Mutex
	made := []*net.UDPConn{}
	dialUDP = func(_ string, _ *net.UDPAddr, raddr *net.UDPAddr) (*net.UDPConn, error) {
		c, err := net.DialUDP("udp", nil, raddr)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		made = append(made, c)
		mu.Unlock()
		return c, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	udpListener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer udpListener.Close() //gosec:disable -- 错误忽略：测试清理

	s := &Server{
		udpListener: udpListener,
		packetQueue: make(chan *tproxyPacketInfo, 16),
		packetChan:  make(chan tunnel.PacketConn, 8),
		timeout:     5 * time.Minute,
		mapping:     make(map[string]*PacketConn),
		ctx:         ctx,
		cancel:      cancel,
	}
	go s.packetDispatchLoop()

	s.packetQueue <- &tproxyPacketInfo{src: src, dst: src, payload: []byte("in")}
	pc, err := s.AcceptPacket(&Tunnel{})
	if err != nil {
		t.Fatalf("AcceptPacket: %v", err)
	}
	session := pc.(*PacketConn)

	// 同一条会话打向 3*上限 个不同目的地：端口不同即键不同
	const sprayed = maxReplySocksPerSession * 3
	for i := range sprayed {
		session.output <- &packetInfo{
			metadata: fakeBindAddress(20000 + i),
			payload:  []byte("p"),
		}
	}
	if n := countPackets(t, peer, sprayed); n != sprayed {
		t.Fatalf("客户端收到 %d 个回包，期望 %d：淘汰路径把在途数据报弄丢了", n, sprayed)
	}

	mu.Lock()
	snapshot := append([]*net.UDPConn(nil), made...)
	mu.Unlock()
	if len(snapshot) != sprayed {
		t.Fatalf("一共拨了 %d 个回包 socket，期望 %d（判据前提：每个目的地各拨一个）", len(snapshot), sprayed)
	}

	live := 0
	for _, c := range snapshot {
		if _, err := c.Write([]byte("probe")); err == nil {
			live++
		}
	}
	if live > maxReplySocksPerSession {
		t.Fatalf("一条会话同时攥着 %d 个回包 socket，上限是 %d：缓存只增不删，"+
			"目的地由对端决定，一条会话就能把 fd 数顶到对端想去的目的地个数", live, maxReplySocksPerSession)
	}

	cancel()
}

// TestUDPSessionCountIsBounded 钉住会话表上限。表原先只按空闲超时清理，条目数
// 等于 UDPTimeout × 新源地址到达速率；源地址是可伪造的，所以这条上限不能只在
// 文档里存在（旧注释就写着"会话表有空闲超时与源地址上限"，代码里根本没有后者）。
func TestUDPSessionCountIsBounded(t *testing.T) {
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
		mapping:     make(map[string]*PacketConn, maxUDPSessions+1),
		ctx:         ctx,
		cancel:      cancel,
	}
	// 把表灌满：会话对象本身不必活着，这里要测的是建表前那一道闸
	srcs := make([]*net.UDPAddr, 0, maxUDPSessions+1)
	for i := 0; i <= maxUDPSessions; i++ {
		src := &net.UDPAddr{IP: net.IPv4(10, 0, 0, byte(i>>8)), Port: 1000 + i}
		srcs = append(srcs, src)
		if i < maxUDPSessions {
			s.mapping[src.String()] = &PacketConn{
				input:  make(chan *packetInfo, udpSessionQueueSize),
				output: make(chan *packetInfo, udpSessionQueueSize),
				ctx:    ctx,
				cancel: func() {},
				src:    src,
			}
		}
	}
	go s.packetDispatchLoop()

	overCap := srcs[maxUDPSessions]
	s.packetQueue <- &tproxyPacketInfo{src: overCap, dst: overCap, payload: []byte("x")}

	// 同步点：投给一条**已存在**的会话。dispatch 是单消费者，这条走到 found 分支
	// 塞进 input 就说明上一条已经判完；表满时新源地址一律不再建会话，所以不能拿
	// "新建一条"当同步点。
	s.packetQueue <- &tproxyPacketInfo{src: srcs[0], dst: srcs[0], payload: []byte("y")}
	select {
	case info := <-sessionQueueOf(s, srcs[0]):
		if len(info.payload) != 1 {
			t.Fatalf("同步点收到的载荷不对：%q", info.payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch 循环没有处理投递的包，判据失效")
	}

	s.mappingLock.RLock()
	size := len(s.mapping)
	_, hasOverCap := s.mapping[overCap.String()]
	s.mappingLock.RUnlock()

	if hasOverCap {
		t.Fatalf("会话表已有 %d 条时仍然为新源地址开了会话：条目数由包量决定，没有上限", maxUDPSessions)
	}
	if size != maxUDPSessions {
		t.Fatalf("会话表 %d 条，上限是 %d：闸门没有卡住", size, maxUDPSessions)
	}
	cancel()
}

// sessionQueueOf 取回预置会话的 input 队列，只给测试当同步点用。
func sessionQueueOf(s *Server, src *net.UDPAddr) chan *packetInfo {
	s.mappingLock.RLock()
	defer s.mappingLock.RUnlock()
	return s.mapping[src.String()].input
}
