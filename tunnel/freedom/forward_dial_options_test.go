package freedom

import (
	"context"
	"net"
	"testing"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// 转发代理那一跳必须是完整的 DialConfig：common.Dial 在返回前无条件执行
// SetKeepAlive(cfg.KeepAlive) 与 SetNoDelay(cfg.NoDelay)，零值等于显式关闭保活、
// 显式打开 Nagle——而不是"沿用系统默认"。旧实现只传 Network/Address/Timeout，
// 配了 noDelay/keepAlive 的部署在 forward-proxy 链路上完全看不到效果。
func TestForwardDialerCarriesSocketOptions(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close() //gosec:disable -- 错误忽略：测试清理
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveSOCKSConnect(c, "ok") //gosec:disable -- 测试夹具
		}
	}()

	origDial := dialForward
	t.Cleanup(func() { dialForward = origDial })
	var captured common.DialConfig
	dialForward = func(ctx context.Context, cfg common.DialConfig) (net.Conn, error) {
		captured = cfg
		return origDial(ctx, cfg)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Client{
		ctx:          ctx,
		cancel:       cancel,
		forwardProxy: true,
		proxyAddr:    proxyAddrFrom(t, ln.Addr()),
		noDelay:      true,
		keepAlive:    true,
	}

	conn, err := c.DialConn(tunnel.NewAddressFromHostPort("tcp", "example.org", 80), nil)
	if err != nil {
		t.Fatalf("DialConn: %v", err)
	}
	defer conn.Close() //gosec:disable -- 错误忽略：测试清理

	if !captured.NoDelay || !captured.KeepAlive {
		t.Fatalf("forward 拨号丢了 socket 选项: NoDelay=%v KeepAlive=%v（零值会被 common.Dial 当成显式关闭）",
			captured.NoDelay, captured.KeepAlive)
	}
}
