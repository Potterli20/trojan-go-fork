//go:build linux

package tproxy

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// TestClosedSessionIsReplacedForSameSource 覆盖 nat 模式 TPROXY 入站的 UDP 会话回收：
// relay 侧调 conn.Close() 只 cancel 会话自己的 ctx，而会话 goroutine 的 select 原本只有
// s.ctx.Done() 和空闲 timer，于是 mapping 里的死表项要挂满 UDPTimeout。这期间同源地址的包
// 全被投进没人读的 input 丢掉，并且 found 恒真、永远不再投递新会话。
//
// 这里不碰真实 tproxy socket：ReadFromUDP 需要 IP_TRANSPARENT（CAP_NET_ADMIN），CI 与本机
// 都没有，所以直接往 s.packetQueue 投伪造的 info，跳开 socket 层验证同一份会话代码。
// udpListener 用普通环回监听，读取协程会一直阻塞在 ReadMsgUDP 上，测试结束时随 ctx 撤掉。
func TestClosedSessionIsReplacedForSameSource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	udpListener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer udpListener.Close()

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

	src := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 45123}
	dst := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 45124}

	s.packetQueue <- &tproxyPacketInfo{src: src, dst: dst, payload: []byte("first")}
	pc, err := s.AcceptPacket(&Tunnel{})
	if err != nil {
		t.Fatalf("AcceptPacket: %v", err)
	}
	buf := make([]byte, 32)
	n, _, err := pc.ReadWithMetadata(buf)
	if err != nil || string(buf[:n]) != "first" {
		t.Fatalf("首包没能送达会话: n=%d err=%v", n, err)
	}

	// relay 拆会话：只关这一条 PacketConn，不动 Server
	if err := pc.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	var fresh tunnel.PacketConn
	for time.Now().Before(deadline) {
		select {
		case s.packetQueue <- &tproxyPacketInfo{src: src, dst: dst, payload: []byte("again")}:
		default:
			time.Sleep(20 * time.Millisecond)
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
		t.Fatal("会话关闭后同源地址收不到新会话：死表项要等 UDPTimeout 才消失，期间的 UDP 包全部黑洞")
	}

	s.mappingLock.Lock()
	current := s.mapping[src.String()]
	s.mappingLock.Unlock()
	if current == nil || tunnel.PacketConn(current) != fresh {
		t.Fatalf("mapping 表项没有换成刚投递的新会话：current=%v fresh=%v", current, fresh)
	}
}
