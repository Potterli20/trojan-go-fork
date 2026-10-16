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

// TestReplySocketIsReusedPerDestination 钉住回包 socket 的复用与回收。
//
// 旧写法是**每个出向包**一次 DialUDP：socket()+IP_TRANSPARENT+bind+connect+
// os.NewFile+FileConn，写完立刻 Close。一条会话几十包就是几十套这套系统调用，
// 外加同样数量的一次性 fd。现在按目的地复用、存活期跟随会话。
//
// 注意 DialUDP 的参数次序：laddr 是**伪造的原始目的地**（TPROXY 要绑上去，
// 客户端才分不出这是代理），raddr 才是客户端。所以测试里的收包端是"会话的源地址"，
// 而 dial 的计数按 laddr 去区分目的地。
//
// 真实 DialUDP 需要 CAP_NET_ADMIN（CI 与本机都没有），因此走 dialUDP 注入点：
// 拨出来的是普通环回连接，"拨了几次、都关掉没有"于是变成可核对的事实。
func TestReplySocketIsReusedPerDestination(t *testing.T) {
	origDial := dialUDP
	t.Cleanup(func() { dialUDP = origDial })

	// 客户端那一侧：会话的 src 必须真有人监听，回包才收得到
	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("peer: %v", err)
	}
	defer peer.Close() //gosec:disable -- 错误忽略：测试清理
	src := peer.LocalAddr().(*net.UDPAddr)

	var mu sync.Mutex
	dialsByBindAddr := map[string]int{}
	made := []*net.UDPConn{}

	dialUDP = func(_ string, laddr *net.UDPAddr, raddr *net.UDPAddr) (*net.UDPConn, error) {
		mu.Lock()
		dialsByBindAddr[laddr.String()]++
		mu.Unlock()
		// 没有 CAP_NET_ADMIN 就绑不上"别人的地址"，这里只连向客户端；
		// 判据关心的是拨号次数与 socket 归属，不是 bind 结果
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
		mapping:     make(map[tunnel.SessionKey]*PacketConn),
		ctx:         ctx,
		cancel:      cancel,
	}
	go s.packetDispatchLoop()

	s.packetQueue <- &tproxyPacketInfo{src: src, dst: src, payload: []byte("in")}
	pc, err := s.AcceptPacket(&Tunnel{})
	if err != nil {
		t.Fatalf("AcceptPacket: %v", err)
	}
	session, ok := pc.(*PacketConn)
	if !ok {
		t.Fatalf("投递的不是 *PacketConn: %T", pc)
	}

	// 同一目的地 3 包 + 另一个目的地 1 包
	dstA := fakeBindAddress(19001)
	dstB := fakeBindAddress(19002)
	for range 3 {
		session.output <- &packetInfo{metadata: dstA, payload: []byte("to-A")}
	}
	session.output <- &packetInfo{metadata: dstB, payload: []byte("to-B")}

	// 四个包都要真的写向客户端
	if n := countPackets(t, peer, 4); n != 4 {
		t.Fatalf("客户端收到 %d 个回包，期望 4", n)
	}

	mu.Lock()
	total := 0
	for _, c := range dialsByBindAddr {
		total += c
	}
	snapshot := append([]*net.UDPConn(nil), made...)
	mu.Unlock()
	if total != 2 {
		t.Fatalf("4 个回包拨了 %d 次 socket：按目的地复用没生效（旧写法每包拨一次）", total)
	}
	if len(snapshot) != 2 {
		t.Fatalf("持有了 %d 个回包 socket，期望 2", len(snapshot))
	}

	// 会话结束：这些 socket 必须全部被关掉，否则只是把每包泄露换成按会话泄露
	if err := session.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for i, c := range snapshot {
		for {
			if _, err := c.Write([]byte("x")); err != nil {
				break // 已经关掉了
			}
			if time.Now().After(deadline) {
				t.Fatalf("第 %d 个回包 socket 在会话结束后仍可用：defer 回收没覆盖这条路径", i)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// fakeBindAddress 造一个"原始目的地"metadata。TPROXY 语义里 laddr 就是它。
func fakeBindAddress(port int) *tunnel.Metadata {
	return &tunnel.Metadata{
		Address: &tunnel.Address{
			AddressType: tunnel.IPv4,
			IP:          net.IPv4(127, 0, 0, 1).To4(),
			Port:        port,
		},
	}
}

// countPackets 读到拿满 want 个包或超时为止，返回实际收到的个数。
func countPackets(t *testing.T, c *net.UDPConn, want int) int {
	t.Helper()
	n := 0
	buf := make([]byte, 512)
	deadline := time.Now().Add(2 * time.Second)
	for n < want && time.Now().Before(deadline) {
		if err := c.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
			t.Fatalf("SetReadDeadline: %v", err)
		}
		if _, _, err := c.ReadFromUDP(buf); err != nil {
			return n
		}
		n++
	}
	return n
}
