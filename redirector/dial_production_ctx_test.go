package redirector

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// 生产入站（trojan/tls/websocket/shadowsocks）构造 Redirection 时都不设 Dial，
// worker 因此走 defaultDial。R10-2 加的 defaultDialContext 虽接 ctx，但 defaultDial
// 硬编码 context.Background()——生产路径根本没把 Redirector 的 ctx 接进拨号。
// 判据用注入点 dialTCPContext 抓生产路径真正传入的 ctx：cancel 之后它必须已结束，
// 否则诱饵是 SYN 黑洞/慢 DNS 时 Redirector.Close 的 wg.Wait 会白等满 30s。
func TestWorkerDefaultDialUsesRedirectorContext(t *testing.T) {
	orig := dialTCPContext
	defer func() { dialTCPContext = orig }()

	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	dialTCPContext = func(ctx context.Context, d *net.Dialer, network, address string) (net.Conn, error) {
		entered <- ctx
		<-release // 卡住拨号，等测试 cancel 后再放
		return nil, errors.New("released")
	}

	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	r := NewRedirector(parent)

	inbound, _ := net.Pipe()
	defer inbound.Close()
	r.Redirect(&Redirection{
		InboundConn: inbound,
		RedirectTo:  &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1},
		// Dial 留空，走生产默认路径
	})

	var usedCtx context.Context
	select {
	case usedCtx = <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("生产默认拨号没有被调用")
	}

	// cancel Redirector 的 ctx（等于 Proxy.Close 调 Redirector.Close 的效果）
	cancelParent()
	close(release)

	if usedCtx.Err() == nil {
		t.Fatal("生产默认拨号拿到的是 context.Background()（或脱离 r.ctx 的 ctx）：" +
			"Redirector 关停后拨号 ctx 仍未结束，Close 会白等拨号超时")
	}
}
