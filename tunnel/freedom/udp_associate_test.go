package freedom

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// serveSOCKSSilent 受理连接、把客户端发来的字节读干净，但**一个握手字节都不回**。
// 上游代理半死（TCP 连上了却卡在协商/应答）就是这个形态。
func serveSOCKSSilent(c net.Conn) {
	defer c.Close() //gosec:disable -- 错误忽略：测试清理
	buf := make([]byte, 256)
	for {
		if _, err := c.Read(buf); err != nil {
			return
		}
	}
}

// TestForwardProxyUDPAssociateIsBounded 钉住 UDP ASSOCIATE 这一跳必须有截止时限。
//
// socks5-fork 只在 TCPTimeout != 0 时才 SetDeadline，而 DialPacket 过去传的是 0，
// 于是"受理 TCP 却不回握手字节"的上游代理让 Negotiate/Request 里的读永不返回。
// 调用方是 relay 的会话协程（proxy/proxy.go 的 relayPacketLoop → sink.DialPacket），
// 一次撞死就永久占用一条协程加一个半开 socket；Client.Close 只 cancel ctx，而这个拨号
// 从头到尾不看 ctx，所以叫不醒它——只能等进程退出兜底。
//
// AGENTS.md 里 forwardDialer 那条修复覆盖的是 DialConn，这条姊妹分支是漏的。
// 时限缩到 300ms（库的粒度是秒，向上取整成 1s，见 socksTimeoutSeconds），
// 正常应当在几秒内带错误返回。正向对照见 TestForwardProxyUDPAssociateSucceeds：
// 把闸门改成"不设截止"的话这条会红，而上一条会挂在 10s 兜底上判死。
func TestForwardProxyUDPAssociateIsBounded(t *testing.T) {
	orig := socksAssociateTimeout
	socksAssociateTimeout = 300 * time.Millisecond
	t.Cleanup(func() { socksAssociateTimeout = orig })
	if got := socksTimeoutSeconds(); got != 1 {
		t.Fatalf("时限换算成库的整秒是 %d：向上取整失效的话缩小的时限会退回 0=不设截止", got)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close() //gosec:disable -- 错误忽略：测试清理
	accepted := make(chan struct{}, 8)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- struct{}{}
			go serveSOCKSSilent(conn) //gosec:disable -- 测试夹具：故意不回任何握手字节
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Client{
		ctx:          ctx,
		cancel:       cancel,
		forwardProxy: true,
		proxyAddr:    proxyAddrFrom(t, ln.Addr()),
	}

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		pc, err := c.DialPacket(&Tunnel{})
		if err == nil && pc != nil {
			_ = pc.Close() //gosec:disable -- 错误忽略：测试清理
		}
		done <- err
	}()

	select {
	case <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("代理侧没有 accept 到这条连接，用例没走到 SOCKS 握手，判据失效")
	}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("上游一个握手字节都没回，DialPacket 竟然报成功")
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("过了 %v 才返回：UDP ASSOCIATE 的握手没有截止时限", elapsed.Round(time.Millisecond))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancel 之后 DialPacket 仍不返回：会话协程被一个不回握手字节的上游代理永久钉住，" +
			"这条协程与它攥着的半开 socket 谁也不会回收")
	}
}

// TestForwardProxyUDPAssociateSucceeds 是上一半的正面对照：给握手加上截止之后，
// 一个会正常应答 UDP ASSOCIATE 的代理仍然必须能用，而且关联建好之后 UDP 数据面
// 不受这条 TCP 截止时间的牵连（负载走 SocksPacketConn 自己的 PacketConn）。
func TestForwardProxyUDPAssociateSucceeds(t *testing.T) {
	// 假代理的 UDP 端点：ASSOCIATE 应答里把这里作为 BADDR/BPORT 交回去
	relay, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer relay.Close() //gosec:disable -- 错误忽略：测试清理
	relayPort := relay.LocalAddr().(*net.UDPAddr).Port

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer ln.Close() //gosec:disable -- 错误忽略：测试清理
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveSOCKSUDPAssociate(conn, relayPort) //gosec:disable -- 测试夹具
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Client{
		ctx:          ctx,
		cancel:       cancel,
		forwardProxy: true,
		proxyAddr:    proxyAddrFrom(t, ln.Addr()),
	}

	pc, err := c.DialPacket(&Tunnel{})
	if err != nil {
		t.Fatalf("对一个正常应答的代理 DialPacket 失败（握手时限把成功的链路掐了？）: %v", err)
	}
	if _, ok := pc.(*SocksPacketConn); !ok {
		t.Fatalf("forward_proxy 下返回了 %T，期望 *SocksPacketConn", pc)
	}
	defer pc.Close() //gosec:disable -- 错误忽略：测试清理

	meta := &tunnel.Metadata{
		Address: &tunnel.Address{
			AddressType: tunnel.IPv4,
			IP:          []byte{8, 8, 8, 8},
			Port:        53,
			NetworkType: "udp",
		},
	}
	payload := []byte("associate happy path")
	if _, err := pc.WriteWithMetadata(payload, meta); err != nil {
		t.Fatalf("WriteWithMetadata: %v", err)
	}

	buf := make([]byte, 4096)
	deadline := time.Now().Add(3 * time.Second)
	if udpErr := relay.SetReadDeadline(deadline); udpErr != nil {
		t.Fatalf("set read deadline: %v", udpErr)
	}
	n, _, err := relay.ReadFrom(buf)
	if err != nil {
		t.Fatalf("假代理的 UDP 端点没收到包（关联建好了却发不出负载）: %v", err)
	}
	// 帧 = RSV(2)+FRAG(1)+地址块+负载；只要求末尾那段是原样负载
	if n < len(payload) || string(buf[n-len(payload):n]) != string(payload) {
		t.Fatalf("收到的帧尾部 %q，期望负载 %q", buf[:n], payload)
	}
}

// serveSOCKSUDPAssociate 走完无认证的方法协商，接受 UDP ASSOCIATE 并把 bind 地址
// 报成 127.0.0.1:relayPort。
func serveSOCKSUDPAssociate(c net.Conn, relayPort int) {
	defer c.Close() //gosec:disable -- 错误忽略：测试清理
	buf := make([]byte, 256)
	if _, err := c.Read(buf); err != nil { // 方法协商
		return
	}
	if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
		return
	}
	if _, err := c.Read(buf); err != nil { // UDP ASSOCIATE 请求
		return
	}
	reply := []byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1,
		byte(relayPort >> 8), byte(relayPort & 0xff)}
	_, _ = c.Write(reply) //gosec:disable -- 测试夹具
}
