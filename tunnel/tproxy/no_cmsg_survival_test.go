//go:build linux

package tproxy

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// tproxy 的 packetDispatchLoop 对所有读错误一视同仁地累加 readErrors，
// 到 10 次就 cancel+关监听自杀。但 ReadFromUDP 的"无 ORIGDST cmsg"错误是
// **包属性**而非 socket 故障：任何直接发到 tproxy 端口、没经过 TPROXY 规则
// 注入 cmsg 的 UDP 包（扫描器直连、本机进程、loopback）都会命中。
// 于是 10 个这样的包（约 1 秒，每次退避 100ms）就能远程打掉整个 tproxy 入站
// （连 TCP 一起关），且无重启路径。对照 socks/dokodemo 遇持续读错误只退避不自杀。
//
// 判据用普通 UDP socket 发无 cmsg 包（不需要 IP_TRANSPARENT）：包会排在 socket
// 缓冲里被读循环逐个取走。旧实现累加到 10 次后 s.ctx 被 cancel；修复后这类
// 包被直接丢弃、不入自杀计数，s.ctx 保持存活。
func TestUDPMissingOriginalDstDoesNotKillServer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	udpListener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer udpListener.Close()
	// closeListeners（自杀路径会调）无条件关 tcpListener，夹具必须真给一个。
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer tcpListener.Close()

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
	go s.packetDispatchLoop()

	sender, err := net.DialUDP("udp", nil, udpListener.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("dial udp: %v", err)
	}
	defer sender.Close()

	// 发 15 个没有 ORIGDST cmsg 的包：足够旧实现撞满自杀阈值（10）。
	for range 15 {
		if _, err := sender.Write([]byte("no-cmsg-packet")); err != nil {
			t.Fatalf("send udp: %v", err)
		}
	}

	// 读循环每次错误退避 100ms，15 个包约 1.5s；等它全部处理完再判定。
	time.Sleep(1800 * time.Millisecond)
	if s.ctx.Err() != nil {
		t.Fatal("无 ORIGDST cmsg 的 UDP 包（可伪造源直连）把 tproxy 入站自杀式关停了：10 包即可远程打掉整个入站")
	}
}
