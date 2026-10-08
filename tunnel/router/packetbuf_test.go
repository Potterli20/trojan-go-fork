package router

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/Potterli20/trojan-go-fork/tunnel/freedom"
)

// scriptedPacketConn 依次产出内容互不相同的 UDP 包，供完就阻塞到 Close。
// 它是专用于检验缓冲区借用协议的夹具：生产端每包从池里借一个 buffer，借用权
// 随 packetInfo 交给消费端；一旦归还时机出错（消费端还没 copy 完就复用同一个
// buffer），这里读到的就是串包内容。
type scriptedPacketConn struct {
	packets [][]byte
	idx     int
	done    chan struct{}
	once    sync.Once
}

// ReadWithMetadata 只被 packetLoop 的单一 goroutine 调用，所以 idx 无需加锁
func (c *scriptedPacketConn) ReadWithMetadata(p []byte) (int, *tunnel.Metadata, error) {
	if c.idx >= len(c.packets) {
		<-c.done
		return 0, nil, io.EOF
	}
	pkt := c.packets[c.idx]
	c.idx++
	n := copy(p, pkt)
	return n, &tunnel.Metadata{
		Address: &tunnel.Address{
			AddressType: tunnel.IPv4,
			IP:          net.IPv4(1, 2, 3, 4).To4(),
			Port:        53,
			NetworkType: "udp",
		},
	}, nil
}

func (c *scriptedPacketConn) WriteWithMetadata(p []byte, m *tunnel.Metadata) (int, error) {
	return len(p), nil
}

func (c *scriptedPacketConn) Close() error {
	c.once.Do(func() { close(c.done) })
	return nil
}

func (c *scriptedPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	<-c.done
	return 0, nil, io.EOF
}

func (c *scriptedPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) { return len(p), nil }
func (c *scriptedPacketConn) LocalAddr() net.Addr                          { return &net.UDPAddr{} }
func (c *scriptedPacketConn) RemoteAddr() net.Addr                         { return &net.UDPAddr{} }
func (c *scriptedPacketConn) SetDeadline(t time.Time) error                { return nil }
func (c *scriptedPacketConn) SetReadDeadline(t time.Time) error            { return nil }
func (c *scriptedPacketConn) SetWriteDeadline(t time.Time) error           { return nil }

type scriptedClient struct{ pc tunnel.PacketConn }

func (c *scriptedClient) DialConn(*tunnel.Address, tunnel.Tunnel) (tunnel.Conn, error) {
	return nil, common.NewError("not supported")
}

func (c *scriptedClient) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return c.pc, nil
}

func (c *scriptedClient) Close() error { return nil }

// TestRouterPacketConnBufferReuseKeepsPacketsIntact 覆盖 UDP 中继的缓冲区归还协议。
// 包尺寸随序号变化（长度借错就会露出来），内容同时编码序号（同一 buffer 被两个
// 在途包共用就会串包）。读完后把调用方 buffer 涂成 0xFF，确保断言的是本次真正
// 拷进来的字节，而不是上一包的残留。
func TestRouterPacketConnBufferReuseKeepsPacketsIntact(t *testing.T) {
	const packetCount = 256
	packets := make([][]byte, packetCount)
	for i := range packets {
		size := 64 + (i%64)*64
		pkt := make([]byte, size)
		pkt[0], pkt[1] = byte(i>>8), byte(i)
		for j := 2; j < size; j++ {
			pkt[j] = byte(i + j)
		}
		packets[i] = pkt
	}

	// router 的 NewClient 会自建 freedom 作为 direct 通路，需要它的配置
	ctx := config.WithConfig(context.Background(), freedom.Name, &freedom.Config{})
	ctx = config.WithConfig(ctx, Name, &Config{
		Router: RouterConfig{
			Enabled:        true,
			DomainStrategy: "AsIs",
			DefaultPolicy:  "proxy",
		},
	})
	client, err := NewClient(ctx, &scriptedClient{pc: &scriptedPacketConn{
		packets: packets,
		done:    make(chan struct{}),
	}})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	conn, err := client.DialPacket(nil)
	if err != nil {
		t.Fatalf("DialPacket failed: %v", err)
	}
	defer conn.Close() //gosec:disable -- 错误忽略：测试清理

	buf := make([]byte, MaxPacketSize)
	for i := 0; i < packetCount; i++ {
		n, meta, err := conn.ReadWithMetadata(buf)
		if err != nil {
			t.Fatalf("第 %d 个包读取失败: %v", i, err)
		}
		if n != len(packets[i]) {
			t.Fatalf("第 %d 个包长度 %d，期望 %d：buffer[:n] 的归还尺寸不对", i, n, len(packets[i]))
		}
		if !bytes.Equal(buf[:n], packets[i]) {
			t.Fatalf("第 %d 个包内容被串：同一个 buffer 交给了多个在途包，池化归还协议被破坏", i)
		}
		if meta == nil || meta.Address.Port != 53 {
			t.Fatalf("第 %d 个包 metadata 异常: %v", i, meta)
		}
		for j := range buf {
			buf[j] = 0xFF
		}
	}
}
