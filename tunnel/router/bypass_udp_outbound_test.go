package router

// R17-3：router 的 UDP 拨号此前用裸 net.ListenPacket("udp", "")，
// 绕过了 freedom 已经实现的全局出站配置（outbound_local_addr / outbound_fwmark）
// 与 prefer_ipv4。策略路由部署下 bypass UDP 的源地址与路由标记与 TCP/代理流量
// 不一致，回包走错路由；prefer_ipv4 配了也无效（仍开双栈 socket）。
//
// 判据用 freedom.SetGlobalOutbound 注入一个回环地址，调 Client.DialPacket，
// 断言拿到的 socket 绑定在该地址上（旧实现绑在 0.0.0.0/::，断言失败）。
// 变异对照：把 router.DialPacket 改回 net.ListenPacket("udp", "")，本用例转红。

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/Potterli20/trojan-go-fork/tunnel/freedom"
)

// eofPacketConn 让 packetLoop 立刻 EOF 退出，避免判据卡在关停上。
type eofPacketConn struct{}

func (eofPacketConn) ReadWithMetadata([]byte) (int, *tunnel.Metadata, error) {
	return 0, nil, io.EOF
}
func (eofPacketConn) WriteWithMetadata([]byte, *tunnel.Metadata) (int, error) {
	return 0, common.NewError("unused")
}
func (eofPacketConn) Close() error                     { return nil }
func (eofPacketConn) LocalAddr() net.Addr              { return nil }
func (eofPacketConn) SetDeadline(time.Time) error      { return nil }
func (eofPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (eofPacketConn) SetWriteDeadline(time.Time) error { return nil }
func (eofPacketConn) ReadFrom([]byte) (int, net.Addr, error) {
	return 0, nil, io.EOF
}
func (eofPacketConn) WriteTo([]byte, net.Addr) (int, error) { return 0, common.NewError("unused") }

type eofClient struct{}

func (eofClient) DialConn(*tunnel.Address, tunnel.Tunnel) (tunnel.Conn, error) {
	return nil, common.NewError("unused")
}
func (eofClient) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return eofPacketConn{}, nil
}
func (eofClient) Close() error { return nil }

func TestDialPacketHonorsGlobalOutboundLocalAddr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	direct, err := freedom.NewClient(config.WithConfig(ctx, freedom.Name, &freedom.Config{}), nil)
	if err != nil {
		t.Fatalf("freedom.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = direct.Close() })

	// NewClient 会用配置重置全局出站状态，所以要在它之后再注入回环地址。
	localIP := net.ParseIP("127.0.0.1")
	prevIP, prevMark := freedom.SetGlobalOutbound(localIP, 0)
	t.Cleanup(func() { freedom.SetGlobalOutbound(prevIP, prevMark) })

	c := &Client{
		ctx:      ctx,
		cancel:   cancel,
		direct:   direct,
		underlay: eofClient{},
	}
	conn, err := c.DialPacket(&Tunnel{})
	if err != nil {
		t.Fatalf("DialPacket: %v", err)
	}
	defer conn.Close() //gosec:disable -- 错误忽略：测试清理

	udpAddr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("LocalAddr 类型=%T，期望 *net.UDPAddr", conn.LocalAddr())
	}
	if !udpAddr.IP.Equal(localIP) {
		t.Fatalf("bypass UDP socket 绑定在 %v，未遵循 outbound_local_addr=%v："+
			"router 用了裸 net.ListenPacket 而非 freedom 的直连拨号", udpAddr.IP, localIP)
	}
}
