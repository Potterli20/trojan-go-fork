package proxy

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// emptyThenPacketConn 依次交出「空数据报」和「5 字节数据报」，然后一直阻塞到 stop 关闭。
// 其余方法走内嵌的 nil 接口：中继在 packet 路径上只会用到这三个，一旦被调用就 panic，
// 这本身就是"用例没走到预期路径"的告警。
type emptyThenPacketConn struct {
	tunnel.PacketConn
	mu       sync.Mutex
	reads    []int
	idx      int
	meta     *tunnel.Metadata
	writes   chan []byte
	stop     chan struct{}
	halfOpen sync.Once
}

func (c *emptyThenPacketConn) ReadWithMetadata(p []byte) (int, *tunnel.Metadata, error) {
	c.mu.Lock()
	if c.idx < len(c.reads) {
		n := c.reads[c.idx]
		c.idx++
		c.mu.Unlock()
		if n > 0 {
			copy(p[:n], []byte("ABCDE"))
		}
		return n, c.meta, nil
	}
	c.mu.Unlock()
	<-c.stop
	return 0, nil, io.EOF
}

func (c *emptyThenPacketConn) WriteWithMetadata(p []byte, _ *tunnel.Metadata) (int, error) {
	select {
	case c.writes <- append([]byte(nil), p...):
	default:
	}
	return len(p), nil
}

func (c *emptyThenPacketConn) Close() error {
	c.halfOpen.Do(func() { close(c.stop) })
	return nil
}

type packetSourceStub struct {
	tunnel.Server
	ctx   context.Context
	given atomic.Bool
	conn  *emptyThenPacketConn
}

func (s *packetSourceStub) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	return nil, io.EOF
}

func (s *packetSourceStub) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	// 只交出那一条会话；之后挂到 ctx 结束，让外层循环走"已取消"分支干净退出。
	if s.given.CompareAndSwap(false, true) {
		return s.conn, nil
	}
	<-s.ctx.Done()
	return nil, io.EOF
}

type packetSinkStub struct {
	tunnel.Client
	out *emptyThenPacketConn
}

func (c *packetSinkStub) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return c.out, nil
}

// TestPacketRelaySurvivesEmptyDatagram 钉的是 UDP 的合法负载形态：空数据报不是会话结束。
// 旧写法把 relay 里的 n==0 当成 EOF，直接 closeDone + return 拆掉整条会话，而且一行日志
// 都没有。dokodemo/socks/tproxy 的 copy(p, payload) 在负载为空时返回 (0,nil)，trojan 的
// packet.go 在远端写 length=0 时同样返回 (0,nil)——本地任意 app 发一个空 datagram 就能
// 自拆会话，客户端模式下远端还能逐包拆。
func TestPacketRelaySurvivesEmptyDatagram(t *testing.T) {
	meta := &tunnel.Metadata{Address: &tunnel.Address{
		AddressType: tunnel.IPv4, IP: net.IPv4(1, 2, 3, 4).To4(), Port: 443}}

	inbound := &emptyThenPacketConn{
		reads:  []int{0, 5}, // 先空包，再正常包
		meta:   meta,
		stop:   make(chan struct{}),
		writes: make(chan []byte, 4),
	}
	outbound := &emptyThenPacketConn{
		reads:  nil,
		meta:   meta,
		stop:   make(chan struct{}),
		writes: make(chan []byte, 4),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := &packetSourceStub{conn: inbound, ctx: ctx}
	sink := &packetSinkStub{out: outbound}

	p := NewProxy(ctx, cancel, []tunnel.Server{src}, sink)
	loopDone := make(chan struct{})
	go func() {
		p.relayPacketLoop()
		close(loopDone)
	}()

	// happens-before：中继把第二个包写出去，测试才继续。不等墙钟。
	select {
	case got := <-outbound.writes:
		if string(got) != "ABCDE" {
			t.Fatalf("转发的负载是 %q，期望 ABCDE", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("空数据报之后的第二个包没被转发：中继在第一个空包处就把整条会话拆了")
	}

	// 先关掉这条会话（让两个方向的读都拿到 EOF），再取消 ctx 叫醒外层循环。
	inbound.Close()
	cancel()
	select {
	case <-loopDone:
	case <-time.After(3 * time.Second):
		t.Fatal("relayPacketLoop 没有退出")
	}
}
