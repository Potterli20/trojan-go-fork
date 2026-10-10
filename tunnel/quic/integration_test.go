package quic

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	tlstunnel "github.com/Potterli20/trojan-go-fork/tunnel/tls"
)

// 这个包此前只有拥塞控制的纯单元测试：tunnel/quic 是从未端到端起过一次的代码。
// 下面两条测试先确认「它到底能不能用」，再确认并发 UDP 会话的归属。

// testPacketSize 与 proxy.testPacketSize 同值；本包没有定义该常量。
const testPacketSize = 8 * 1024

type quicPair struct {
	server *Server
	client *Client
}

func newQuicPair(t *testing.T) *quicPair {
	t.Helper()
	certPath, keyPath := writeSelfSigned(t, t.TempDir())
	port := common.PickPort("udp", "127.0.0.1")

	sctx := quicServerCtx(t, port, certPath, keyPath)
	server, err := NewServer(sctx, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { server.Close() }) //gosec:disable -- 错误忽略：测试清理

	cctx := config.WithConfig(context.Background(), Name, &Config{
		RemoteHost: "127.0.0.1",
		RemotePort: port,
		QUIC: QUICConfig{
			ALPN:               "hq-29",
			MaxIdleTimeout:     30,
			MaxIncomingStreams: 100,
			Congestion:         "bbr",
			Insecure:           true, // 自签证书
		},
	})
	cctx = config.WithConfig(cctx, tlstunnel.Name, &tlstunnel.Config{
		TLS: tlstunnel.TLSConfig{Verify: false, SNI: "localhost"},
	})
	client, err := NewClient(cctx, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { client.Close() }) //gosec:disable -- 错误忽略：测试清理
	return &quicPair{server: server, client: client}
}

// TestQuicStreamRoundTrip 走一遍最基础的 TCP-over-QUIC：服务端 accept 一条 stream
// 原样回显，客户端 dial 一条 stream 写入再读回。
func TestQuicStreamRoundTrip(t *testing.T) {
	pair := newQuicPair(t)

	payload := make([]byte, 4096)
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("随机负载失败: %v", err)
	}

	echoDone := make(chan error, 1)
	go func() {
		conn, err := pair.server.AcceptConn(nil)
		if err != nil {
			echoDone <- fmt.Errorf("AcceptConn: %w", err)
			return
		}
		defer conn.Close() //gosec:disable -- 错误忽略：测试清理
		buf := make([]byte, len(payload))
		if _, err := io.ReadFull(conn, buf); err != nil {
			echoDone <- fmt.Errorf("server read: %w", err)
			return
		}
		if !bytes.Equal(buf, payload) {
			echoDone <- fmt.Errorf("服务端收到的内容与发出的一致性问题: got %d bytes", len(buf))
			return
		}
		if _, err := conn.Write(buf); err != nil {
			echoDone <- fmt.Errorf("server write: %w", err)
			return
		}
		echoDone <- nil
	}()

	clientDone := make(chan error, 1)
	go func() {
		conn, err := pair.client.DialConn(&tunnel.Address{
			AddressType: tunnel.DomainName, DomainName: "example.com", Port: 443,
		}, nil)
		if err != nil {
			clientDone <- fmt.Errorf("DialConn: %w", err)
			return
		}
		defer conn.Close() //gosec:disable -- 错误忽略：测试清理
		if _, err := conn.Write(payload); err != nil {
			clientDone <- fmt.Errorf("client write: %w", err)
			return
		}
		buf := make([]byte, len(payload))
		if _, err := io.ReadFull(conn, buf); err != nil {
			clientDone <- fmt.Errorf("client read: %w", err)
			return
		}
		if !bytes.Equal(buf, payload) {
			clientDone <- fmt.Errorf("回显内容不一致")
			return
		}
		clientDone <- nil
	}()

	select {
	case err := <-clientDone:
		if err != nil {
			t.Fatalf("客户端侧: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("stream 往返超时：tunnel/quic 的 TCP 路径跑不通")
	}
	select {
	case err := <-echoDone:
		if err != nil {
			t.Fatalf("服务端侧: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("服务端回显 goroutine 超时")
	}
}

// TestQuicSingleDatagramRoundTrip 确认 UDP 路径本身可用：客户端 DialPacket 发出
// 一个数据报，服务端 AcceptPacket 取出后原样回发。
func TestQuicSingleDatagramRoundTrip(t *testing.T) {
	pair := newQuicPair(t)
	addr := &tunnel.Address{AddressType: tunnel.DomainName, DomainName: "dns.example.com", Port: 53}
	payload := []byte("hello-quic-datagram")

	acceptDone := make(chan error, 1)
	go func() {
		pc, err := pair.server.AcceptPacket(nil)
		if err != nil {
			acceptDone <- fmt.Errorf("AcceptPacket: %w", err)
			return
		}
		defer pc.Close() //gosec:disable -- 错误忽略：测试清理
		buf := make([]byte, testPacketSize)
		n, _, err := pc.ReadWithMetadata(buf)
		if err != nil {
			acceptDone <- fmt.Errorf("server ReadWithMetadata: %w", err)
			return
		}
		got := append([]byte(nil), buf[:n]...)
		if !bytes.Equal(got, payload) {
			acceptDone <- fmt.Errorf("服务端收到 %q，期望 %q", got, payload)
			return
		}
		if _, err := pc.WriteWithMetadata(got, &tunnel.Metadata{Address: addr}); err != nil {
			acceptDone <- fmt.Errorf("server WriteWithMetadata: %w", err)
			return
		}
		acceptDone <- nil
	}()

	readDone := make(chan error, 1)
	go func() {
		pc, err := pair.client.DialPacket(nil)
		if err != nil {
			readDone <- fmt.Errorf("DialPacket: %w", err)
			return
		}
		defer pc.Close() //gosec:disable -- 错误忽略：测试清理
		if _, err := pc.WriteWithMetadata(payload, &tunnel.Metadata{Address: addr}); err != nil {
			readDone <- fmt.Errorf("client write: %w", err)
			return
		}
		buf := make([]byte, testPacketSize)
		deadline := time.Now().Add(20 * time.Second)
		for {
			n, _, err := pc.ReadWithMetadata(buf)
			if err != nil {
				readDone <- fmt.Errorf("client ReadWithMetadata: %w", err)
				return
			}
			if n == 0 {
				// keepalive 的空数据报也会走到这条读路径上
				if time.Now().After(deadline) {
					readDone <- fmt.Errorf("一直被空数据报占据，读不到内容")
					return
				}
				continue
			}
			if !bytes.Equal(buf[:n], payload) {
				readDone <- fmt.Errorf("客户端收到 %q，期望 %q", buf[:n], payload)
				return
			}
			readDone <- nil
			return
		}
	}()

	select {
	case err := <-readDone:
		if err != nil {
			t.Fatalf("客户端 UDP 侧: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("数据报往返超时：tunnel/quic 的 UDP 路径跑不通")
	}
	select {
	case err := <-acceptDone:
		if err != nil {
			t.Fatalf("服务端 UDP 侧: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("服务端 AcceptPacket goroutine 超时")
	}
}
