package tls

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/Potterli20/trojan-go-fork/tunnel/transport"
)

// reuse-session 过去只设了 SessionTicketsDisabled（服务端字段），客户端没有
// ClientSessionCache，于是第二次握手永远拿不到 DidResume——配置项空转。
func TestSessionResume(t *testing.T) {
	cert, err := tls.X509KeyPair([]byte(rsa2048Cert), []byte(rsa2048Key))
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() }) //gosec:disable -- 错误忽略：测试清理
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close() //gosec:disable -- 错误忽略：测试清理
				tc := c.(*tls.Conn)
				if err := tc.Handshake(); err != nil {
					return
				}
				// 握手后吐一个字节：客户端必须真的 Read 一次，TLS 1.3 的
				// NewSessionTicket 才会被处理进缓存。
				if _, err := tc.Write([]byte("k")); err != nil {
					return
				}
				io.Copy(io.Discard, tc) //gosec:disable -- 错误忽略：测试清理
			}(c)
		}
	}()

	clientCfg := &Config{TLS: TLSConfig{Verify: false, SNI: "localhost", ReuseSession: true}}
	c, err := NewClient(config.WithConfig(context.Background(), Name, clientCfg), nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	addr := tunnel.NewAddressFromHostPort("tcp", host, port)

	for i := 0; i < 2; i++ {
		conn, err := c.DialConn(addr, nil)
		if err != nil {
			t.Fatalf("第 %d 次 DialConn: %v", i+1, err)
		}
		tc := conn.(*transport.Conn).Conn.(*tls.Conn)
		var b [1]byte
		if _, err := conn.Read(b[:]); err != nil {
			t.Fatalf("第 %d 次读取被叫醒的数据失败: %v", i+1, err)
		}
		state := tc.ConnectionState()
		if i == 1 && !state.DidResume {
			conn.Close() //gosec:disable -- 错误忽略：测试清理
			t.Fatal("第二次握手没有复用会话：ReuseSession 配置空转（客户端缺 ClientSessionCache）")
		}
		conn.Close() //gosec:disable -- 错误忽略：测试清理
	}
}

// 已取消的 ctx 拨号，握手必须立刻返回错误，而不是等满 30s 握手时限。
func TestHandshakeHonorsContext(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() }) //gosec:disable -- 错误忽略：测试清理
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// 受理后一个字节都不发：TLS 握手会阻塞在读 ServerHello 上。
			go io.Copy(io.Discard, c) //gosec:disable -- 错误忽略：测试清理
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clientCfg := &Config{TLS: TLSConfig{Verify: false, SNI: "localhost"}}
	c, err := NewClient(config.WithConfig(ctx, Name, clientCfg), nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	cancel()

	addr := tunnel.NewAddressFromHostPort("tcp", "127.0.0.1", ln.Addr().(*net.TCPAddr).Port)
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := c.DialConn(addr, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ctx 已取消，握手竟然报成功")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("错误链里没有 context.Canceled：%v", err)
		}
		if elapsed := time.Since(start); elapsed > 3*time.Second {
			t.Fatalf("取消 ctx 后握手拖了 %v 才返回：没有接回 ctx", elapsed.Round(time.Millisecond))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("取消 ctx 后 DialConn 仍不返回：握手没有接 ctx，只能等满 30s 时限")
	}
}