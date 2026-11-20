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

// 端到端判据：构造一个带 Zone 的 IPv6 目的地 metadata，经会话守护协程的
// 回包路径发出去，注入的 dialUDP 必须看到 laddr（伪造的原始目的地）带着
// 同一个 Zone。udpAddrToSocketAddr 会把它转成 SockaddrInet6.ZoneId，
// 缺了就绑到错误接口。CI 无 CAP_NET_ADMIN，所以走 dialUDP 注入点，
// 拨出来的是普通环回连接，判据只关心 laddr 参数。
func TestReplyDialCarriesZoneToBindAddress(t *testing.T) {
	origDial := dialUDP
	t.Cleanup(func() { dialUDP = origDial })

	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("peer: %v", err)
	}
	defer peer.Close()
	src := peer.LocalAddr().(*net.UDPAddr)

	var mu sync.Mutex
	var capturedZone string
	captured := make(chan struct{}, 1)
	dialUDP = func(_ string, laddr *net.UDPAddr, raddr *net.UDPAddr) (*net.UDPConn, error) {
		mu.Lock()
		if laddr.Zone != "" {
			capturedZone = laddr.Zone
			select {
			case captured <- struct{}{}:
			default:
			}
		}
		mu.Unlock()
		return net.DialUDP("udp", nil, raddr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Server{
		udpListener: mustUDPListener(t),
		tcpListener: mustTCPListener(t),
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
	// 发一个带 Zone 的链路本地目的地回包。
	pc.WriteWithMetadata([]byte("out"), &tunnel.Metadata{Address: &tunnel.Address{
		AddressType: tunnel.IPv6,
		IP:          net.ParseIP("fe80::1"),
		Port:        443,
		Zone:        "eth7",
	}})

	select {
	case <-captured:
	case <-time.After(2 * time.Second):
		t.Fatal("dialUDP 的 laddr 没带 Zone：回包 socket 会绑到错误接口")
	}
	mu.Lock()
	got := capturedZone
	mu.Unlock()
	if got != "eth7" {
		t.Fatalf("传给 dialUDP 的 Zone=%q，期望 eth7", got)
	}
}

func mustUDPListener(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func mustTCPListener(t *testing.T) net.Listener {
	t.Helper()
	c, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
