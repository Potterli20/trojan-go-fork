package quic

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"

	"github.com/apernet/quic-go"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// TestCachedConnectionIsInvalidatedAfterDatagramReadFailure 盯 UDP 永久失联这条链。
//
// 缓存里只存一条 *quic.Conn，而唯一的失效点过去在 DialConn 的 OpenStreamSync 失败
// 分支上：对端重启、NAT 重绑、空闲超时之后，DialPacket 照样把那条死连接交出去，
// ReceiveDatagram 立刻报错 → 会话秒断 → 再拨还是同一条，UDP 通道永远不会自愈
// （TCP 方向反倒会，因为它走那条失效点）。
//
// 判据：读路径拿到连接级错误之后缓存必须被作废，并且下一拨能拿到一条新连接。
func TestCachedConnectionIsInvalidatedAfterDatagramReadFailure(t *testing.T) {
	pair := newQuicPair(t)
	c := pair.client

	pc, err := c.DialPacket(nil)
	if err != nil {
		t.Fatalf("DialPacket: %v", err)
	}
	cached := c.getQuicConn()
	if cached == nil {
		t.Fatal("DialPacket 之后缓存里没有连接")
	}

	// 模拟对端消失：本端把这条连接拆掉，之后任何 ReceiveDatagram 只会拿到连接级错误
	cached.CloseWithError(quic.ApplicationErrorCode(42), "test: peer gone")

	buf := make([]byte, testPacketSize)
	if _, _, err := pc.(*PacketConn).ReadWithMetadata(buf); err == nil {
		t.Fatal("连接已死却仍读到了数据报，前置条件不成立")
	}
	if got := c.getQuicConn(); got == cached {
		t.Fatal("读路径的连接级错误没有作废缓存：下一路 UDP 仍会拿到这条死连接，永久失联")
	}

	if _, err := c.DialPacket(nil); err != nil {
		t.Fatalf("作废之后重拨失败: %v", err)
	}
	if c.getQuicConn() == nil {
		t.Fatal("重拨后缓存仍然是空的")
	}
}

// TestOversizedDatagramIsNotSilentlyTruncated 钉住整包语义。
//
// 旧写法 `n := copy(p, data)` 不检查长度：调用方缓冲装不下时，把截断的一半当完整包
// 交出去，应用层看到的是一个内容被咬掉的"完整"数据报（丢数据且没有任何迹象）。
// QUIC 数据报是整包语义，装不下就必须报错拆会话——与 trojan/packet.go 对超长包的口径一致。
func TestOversizedDatagramIsNotSilentlyTruncated(t *testing.T) {
	ch := make(chan []byte, 4)
	pc := &PacketConn{packetBuffer: ch, done: make(chan struct{})}
	buf := make([]byte, 16)

	ch <- bytes.Repeat([]byte{0xAB}, 64)
	if _, _, err := pc.ReadWithMetadata(buf); err == nil {
		t.Fatal("64 字节的数据报被塞进 16 字节的缓冲当完整包交出去了")
	}

	// 装得下时照常交付，且不能被上一条错误连带
	ch <- []byte("ok")
	n, meta, err := pc.ReadWithMetadata(buf)
	if err != nil {
		t.Fatalf("正常尺寸的数据报读取失败: %v", err)
	}
	if n != 2 || string(buf[:n]) != "ok" {
		t.Fatalf("交付内容不对: n=%d got=%q", n, buf[:n])
	}
	if meta == nil {
		t.Fatal("没有返回 metadata")
	}
}

// TestPacketConnCloseSignalsDone 是服务端那个泵的退出凭据：
// 服务端形态没有 ctx/cancel，Close 过去是空操作，泵没有任何出口。
func TestPacketConnCloseSignalsDone(t *testing.T) {
	ch := make(chan []byte, 1)
	pc := &PacketConn{packetBuffer: ch, done: make(chan struct{})}

	select {
	case <-pc.done:
		t.Fatal("还没 Close 就已经退出信号亮了")
	default:
	}

	common.Must(pc.Close())
	select {
	case <-pc.done:
	default:
		t.Fatal("Close 没有点亮 done：服务端形态的包泵没有任何退出路径")
	}

	// 重复 Close 不能 panic（relay 与 tunnel 两侧都可能关）
	common.Must(pc.Close())
}

// TestUDPSessionCloseKeepsStreamsAlive 钉住这条修复自身的副作用边界。
//
// 泵退出时会点 packetDone，而 handleConnection 见到 packetDone 就 return，它的 defer
// 会把**整个连接** CloseWithError——殃及同一条连接上还在正常工作的 stream。所以
// packetDone 只能在"连接级结束"时点，会话级关闭不行。
//
// 判据用连接身份：先建 UDP 会话、关掉、洪泛 40 个数据报（超过 packetBuffer 的 16），
// 再在同一条连接上拨一条 stream；服务端看到的必须是**同一个 *quic.Conn**。
// 一旦连接被误拆，客户端只能重拨一条新连接，指针就不等了。
func TestUDPSessionCloseKeepsStreamsAlive(t *testing.T) {
	pair := newQuicPair(t)
	md := &tunnel.Metadata{Address: &tunnel.Address{AddressType: tunnel.IPv4, IP: net.IPv4(1, 1, 1, 1), Port: 53}}

	cpc, err := pair.client.DialPacket(nil)
	if err != nil {
		t.Fatalf("client DialPacket: %v", err)
	}

	// 服务端只有在收到第一个数据报之后才会把这条会话投递出来（handlerSent 的语义），
	// 所以 AcceptPacket 必须与客户端的写并发进行
	accepted := make(chan tunnel.PacketConn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		pc, err := pair.server.AcceptPacket(nil)
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- pc
	}()
	if _, err := cpc.WriteWithMetadata([]byte("hello"), md); err != nil {
		t.Fatalf("client WriteWithMetadata: %v", err)
	}
	var spc tunnel.PacketConn
	select {
	case spc = <-accepted:
	case err := <-acceptErr:
		t.Fatalf("server AcceptPacket: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("服务端一直没有投递出 UDP 会话")
	}
	udpConn := spc.(*PacketConn).conn

	probe := make([]byte, testPacketSize)
	if _, _, err := spc.ReadWithMetadata(probe); err != nil {
		t.Fatalf("server ReadWithMetadata: %v", err)
	}

	// 关掉 UDP 会话，然后洪泛：旧代码里泵会卡在 packetBuffer 上
	common.Must(spc.Close())
	flood := []byte("flood")
	for range 40 {
		if _, err := cpc.WriteWithMetadata(flood, md); err != nil {
			break // 连接级失败由下一条判据负责揭示
		}
	}

	streamDone := make(chan error, 1)
	go func() {
		conn, err := pair.server.AcceptConn(nil)
		if err != nil {
			streamDone <- err
			return
		}
		defer conn.Close() //gosec:disable -- 错误忽略：测试清理
		if got := conn.(*StreamConn).conn; got != udpConn {
			streamDone <- common.NewErrorf("stream 落在了一条新连接上：UDP 会话关闭时把整个连接拆了")
			return
		}
		b := make([]byte, 4)
		if _, err := io.ReadFull(conn, b); err != nil {
			streamDone <- err
			return
		}
		_, err = conn.Write(b)
		streamDone <- err
	}()

	clientConn, err := pair.client.DialConn(&tunnel.Address{
		AddressType: tunnel.DomainName, DomainName: "example.com", Port: 443,
	}, nil)
	if err != nil {
		t.Fatalf("client DialConn: %v", err)
	}
	defer clientConn.Close() //gosec:disable -- 错误忽略：测试清理
	if _, err := clientConn.Write([]byte("ping")); err != nil {
		t.Fatalf("client write: %v", err)
	}
	echo := make([]byte, 4)
	if _, err := io.ReadFull(clientConn, echo); err != nil {
		t.Fatalf("client read: %v", err)
	}
	select {
	case err := <-streamDone:
		if err != nil {
			t.Fatalf("同连接的 stream 没能活下来: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("服务端 stream 处理没有收尾")
	}
	if !bytes.Equal(echo, []byte("ping")) {
		t.Fatalf("回显不一致: %q", echo)
	}
}
