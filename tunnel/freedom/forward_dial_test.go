package freedom

import (
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// TestForwardProxyDialHonorsContext 要求 forward-proxy 的拨号被 c.ctx 管住。
// 旧写法把这一跳整个交给 x/net/proxy 的默认 dialer：没有超时、不看 ctx。上游代理
// 收下 TCP 却不回握手字节（半死的链路就是这个形态）时 DialConn 永不返回，而它的
// 调用方是 relay 的会话协程——一次撞死就永久占用一条协程加一个半开 socket，
// Proxy.Close 的 5s waitBounded 只是放弃等待，叫不醒它。
func TestForwardProxyDialHonorsContext(t *testing.T) {
	// 受理之后一个字节都不回的"代理"
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close() //gosec:disable -- 错误忽略：测试清理
	accepted := make(chan struct{}, 8)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- struct{}{}
			go func(c net.Conn) {
				buf := make([]byte, 64)
				for {
					if _, err := c.Read(buf); err != nil {
						c.Close() //gosec:disable -- 错误忽略：测试清理
						return
					}
				}
			}(c) //gosec:disable -- 测试夹具：故意不回任何握手字节
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
	go func() {
		_, err := c.DialConn(tunnel.NewAddressFromHostPort("tcp", "example.org", 80), nil)
		done <- err
	}()

	select {
	case <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("代理侧没有 accept 到这条连接，用例没走到 SOCKS 握手，判据失效")
	}
	time.Sleep(100 * time.Millisecond) // 让握手的读确实阻塞起来

	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("上游一个握手字节都没回，DialConn 竟然报成功")
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("取消 ctx 之后过了 %v 拨号才返回：阻塞中的握手没有被 c.ctx 打断", elapsed.Round(time.Millisecond))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancel 之后 DialConn 仍不返回：会话协程被一个不回握手的上游代理永久钉住，" +
			"这条协程与它攥着的半开 socket 谁也不会回收")
	}
}

// TestForwardProxyHandshakeDeadlineIsCleared 是上一半的正面对照：给握手挂的截止
// 时间必须在交出隧道前解除，否则一条长期会话会在时限到点时被凭空掐断。
// 时限缩到 300ms，代理在握手成功后 600ms 才吐第一个字节——残留的截止会让这次读
// 立刻失败，解除了才读得到。
func TestForwardProxyHandshakeDeadlineIsCleared(t *testing.T) {
	const payload = "post-handshake payload"
	orig := forwardHandshakeTimeout
	forwardHandshakeTimeout = 300 * time.Millisecond
	t.Cleanup(func() { forwardHandshakeTimeout = orig })

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
			go serveSOCKSConnect(c, payload) //gosec:disable -- 测试夹具
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
	conn, err := c.DialConn(tunnel.NewAddressFromHostPort("tcp", "example.org", 80), nil)
	if err != nil {
		t.Fatalf("DialConn: %v", err)
	}
	defer conn.Close() //gosec:disable -- 错误忽略：测试清理

	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("握手成功后的第一次读失败（%v）：握手的截止时间没有解除，"+
			"这条隧道会被自己没做完的握手时限掐断", err)
	}
	if string(buf) != payload {
		t.Fatalf("读到 %q，期望 %q", buf, payload)
	}
}

// serveSOCKSConnect 走完无认证的方法协商、接受 CONNECT，然后在 600ms 之后
// （刻意晚于测试里缩小的握手时限）把 payload 吐回去。
func serveSOCKSConnect(c net.Conn, payload string) {
	defer c.Close() //gosec:disable -- 错误忽略：测试清理
	buf := make([]byte, 256)
	if _, err := c.Read(buf); err != nil { // 方法协商
		return
	}
	if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
		return
	}
	if _, err := c.Read(buf); err != nil { // CONNECT 请求
		return
	}
	reply := []byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	if _, err := c.Write(reply); err != nil {
		return
	}
	time.Sleep(600 * time.Millisecond)
	_, _ = c.Write([]byte(payload)) //gosec:disable -- 测试夹具
}

func proxyAddrFrom(t *testing.T, a net.Addr) *tunnel.Address {
	t.Helper()
	host, port, err := net.SplitHostPort(a.String())
	if err != nil {
		t.Fatalf("split %v: %v", a, err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("port %q: %v", port, err)
	}
	return tunnel.NewAddressFromHostPort("tcp", host, n)
}
