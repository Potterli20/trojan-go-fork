package quic

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"sync"
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

// TestQuicEmptyDatagramDoesNotEndTheReadPath 复现 keepalive 杀死 UDP 会话的根因：
// keepAliveLoop 每 10s 发一个真实的空数据报（client.go:140），而 quic-go 对空 payload
// 没有长度校验（connection.go:3092 只在超长时拒绝），于是它会作为一个正常数据报送达
// 对端。读路径此前没有任何一层过滤零长度数据报：上层 trojan 会在 packet.go:81 解析
// 地址失败并关闭这条会话，proxy.go:318/343 也把 n==0 直接判为会话结束。
// 这里不发真实的 keepalive（那要等 10s），而是手工塞一个空数据报走完全相同的代码路径。
func TestQuicEmptyDatagramDoesNotEndTheReadPath(t *testing.T) {
	pair := newQuicPair(t)
	addr := &tunnel.Address{AddressType: tunnel.DomainName, DomainName: "ka.example.com", Port: 53}

	srvRead := make(chan string, 4)
	go func() {
		pc, err := pair.server.AcceptPacket(nil)
		if err != nil {
			srvRead <- "accept-error"
			return
		}
		defer pc.Close() //gosec:disable -- 错误忽略：测试清理
		buf := make([]byte, testPacketSize)
		for {
			n, _, err := pc.ReadWithMetadata(buf)
			if err != nil {
				srvRead <- "read-error"
				return
			}
			srvRead <- fmt.Sprintf("n=%d:%q", n, buf[:n])
		}
	}()

	pc, err := pair.client.DialPacket(nil)
	if err != nil {
		t.Fatalf("DialPacket: %v", err)
	}
	defer pc.Close() //gosec:disable -- 错误忽略：测试清理

	// 先用一个真实包把服务端的 PacketConn 触发出来（服务端只在收到首个数据报后投递一次）
	if _, err := pc.WriteWithMetadata([]byte("first"), &tunnel.Metadata{Address: addr}); err != nil {
		t.Fatalf("发送 first 失败: %v", err)
	}
	select {
	case got := <-srvRead:
		if got != `n=5:"first"` {
			t.Fatalf("服务端第一个包读到 %s，期望 n=5:\"first\"", got)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("服务端没读到第一个包")
	}

	// 空数据报（等价于 keepalive 发出去的那个），随后紧跟一个真实包
	if _, err := pc.WriteWithMetadata([]byte{}, &tunnel.Metadata{Address: addr}); err != nil {
		t.Fatalf("发送空数据报失败: %v", err)
	}
	if _, err := pc.WriteWithMetadata([]byte("second"), &tunnel.Metadata{Address: addr}); err != nil {
		t.Fatalf("发送 second 失败: %v", err)
	}

	deadline := time.After(20 * time.Second)
	for {
		select {
		case got := <-srvRead:
			switch got {
			case `n=0:""`:
				t.Fatalf("空数据报被当成一次正常读交给了上层（n==0）——按 proxy.go:318 与 " +
					"trojan/packet.go:81 的语义，这条 UDP 会话就此结束")
			case `n=6:"second"`:
				return // 修复生效：空数据报被过滤，会话继续
			case "read-error", "accept-error":
				t.Fatalf("服务端读路径在空数据报后终止（%s），UDP 会话被杀死", got)
			default:
				t.Fatalf("读到非预期的内容 %s", got)
			}
		case <-deadline:
			t.Fatal("没读到 second：空数据报之后的包没有送达")
		}
	}
}

// TestQuicConnFieldIsGuardedByMutex 在真实拨号期间不停读 quicConn 字段。
// 两种错法都会被它抓到：赋值不在临界区内 -> -race 报 DATA RACE；赋值处再调一次
// 加锁 setter -> sync.RWMutex 不可重入，拨号直接死锁（第一版就死在这里）。
func TestQuicConnFieldIsGuardedByMutex(t *testing.T) {
	pair := newQuicPair(t)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					_ = pair.client.getQuicConn()
				}
			}
		})
	}

	dialed := make(chan error, 1)
	go func() {
		conn, err := pair.client.DialConn(&tunnel.Address{
			AddressType: tunnel.DomainName, DomainName: "guard.example.com", Port: 443,
		}, nil)
		if err == nil {
			conn.Close() //gosec:disable -- 错误忽略：测试清理
		}
		dialed <- err
	}()

	select {
	case err := <-dialed:
		close(stop)
		wg.Wait()
		if err != nil {
			t.Fatalf("拨号失败: %v", err)
		}
	case <-time.After(30 * time.Second):
		close(stop)
		t.Fatal("拨号卡住（quicConn 上的加锁写存在重入死锁）")
	}
}

// TestQuicKeepAliveIsInvisibleToDatagrams 守住保活的正确形态：
// MaxIdleTimeout=4s 时 quic-go 每约 2s 发一次 PING 控制帧。空闲 6s 期间服务端
// 必须一个数据报都读不到（既不出现 n==0 也不报错），并且连接仍然活着——
// 之后那个真实包还能送达。旧的"每 10s 发空数据报"实现会在这里读到一个 n==0。
func TestQuicKeepAliveIsInvisibleToDatagrams(t *testing.T) {
	certPath, keyPath := writeSelfSigned(t, t.TempDir())
	port := common.PickPort("udp", "127.0.0.1")
	server, err := NewServer(quicServerCtx(t, port, certPath, keyPath), nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close() //gosec:disable -- 错误忽略：测试清理

	cctx := config.WithConfig(context.Background(), Name, &Config{
		RemoteHost: "127.0.0.1",
		RemotePort: port,
		QUIC:       QUICConfig{ALPN: "hq-29", MaxIdleTimeout: 4, MaxIncomingStreams: 100, Congestion: "bbr", Insecure: true},
	})
	cctx = config.WithConfig(cctx, tlstunnel.Name, &tlstunnel.Config{
		TLS: tlstunnel.TLSConfig{Verify: false, SNI: "localhost"},
	})
	client, err := NewClient(cctx, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close() //gosec:disable -- 错误忽略：测试清理

	addr := &tunnel.Address{AddressType: tunnel.DomainName, DomainName: "idle.example.com", Port: 53}
	srv := make(chan string, 8)
	go func() {
		pc, err := server.AcceptPacket(nil)
		if err != nil {
			srv <- "accept-error"
			return
		}
		defer pc.Close() //gosec:disable -- 错误忽略：测试清理
		buf := make([]byte, testPacketSize)
		for {
			n, _, err := pc.ReadWithMetadata(buf)
			if err != nil {
				srv <- "read-error"
				return
			}
			srv <- fmt.Sprintf("n=%d:%q", n, buf[:n])
		}
	}()

	pc, err := client.DialPacket(nil)
	if err != nil {
		t.Fatalf("DialPacket: %v", err)
	}
	defer pc.Close() //gosec:disable -- 错误忽略：测试清理
	if _, err := pc.WriteWithMetadata([]byte("open"), &tunnel.Metadata{Address: addr}); err != nil {
		t.Fatalf("发送 open 失败: %v", err)
	}
	select {
	case got := <-srv:
		if got != `n=4:"open"` {
			t.Fatalf("首个包读到 %s", got)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("服务端没读到首个包")
	}

	// 空闲 12s。这个长度是有意的：旧的实现是"每 10s 发一个空数据报"，窗口短于 10s
	// 就永远等不到它，门禁会在有 bug 的代码上照样绿。
	select {
	case got := <-srv:
		t.Fatalf("空闲期间读到了 %s：保活产生了应用可见的数据报", got)
	case <-time.After(12 * time.Second):
	}

	// 连接必须还活着
	if _, err := pc.WriteWithMetadata([]byte("after-idle"), &tunnel.Metadata{Address: addr}); err != nil {
		t.Fatalf("空闲后发送失败（连接已被保活机制搞死？）: %v", err)
	}
	select {
	case got := <-srv:
		if got != `n=10:"after-idle"` {
			t.Fatalf("空闲后读到 %s", got)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("空闲之后包没送达：连接已经死了")
	}
}

// TestQuicWindowConfigReachesQuicConfig 接线 initial_stream_window /
// initial_conn_window：这两个键此前被静默忽略（config.go 定义了字段，但从未进入
// quic.Config），用户设了也没有任何效果。
//
// 同时守住"默认不得缩小窗口"这条：quic-go 在值为 0 时使用库内默认 512KB
// （interface.go:125-139），而本仓库旧默认是 65535。要是把 65535 直接接上去，
// 就等于在一次"让配置生效"的改动里顺手把所有人的默认流控窗口缩到 1/8。
// 因此默认取 0，把决定权交回库。
func TestQuicWindowConfigReachesQuicConfig(t *testing.T) {
	certPath, keyPath := writeSelfSigned(t, t.TempDir())
	port := common.PickPort("udp", "127.0.0.1")

	const (
		wantStream uint64 = 1 << 20
		wantConn   uint64 = 1 << 21
	)

	sctx := quicServerCtx(t, port, certPath, keyPath)
	sc := config.FromContext(sctx, Name).(*Config)
	sc.QUIC.InitialStreamWindow = int(wantStream)
	sc.QUIC.InitialConnWindow = int(wantConn)
	server, err := NewServer(sctx, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close() //gosec:disable -- 错误忽略：测试清理
	if server.quicConfig.InitialStreamReceiveWindow != wantStream ||
		server.quicConfig.InitialConnectionReceiveWindow != wantConn {
		t.Fatalf("服务端窗口未接线：stream=%d conn=%d，期望 %d/%d",
			server.quicConfig.InitialStreamReceiveWindow,
			server.quicConfig.InitialConnectionReceiveWindow, wantStream, wantConn)
	}

	client, err := NewClient(clientCtxWithWindows(t, port, wantStream, wantConn), nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close() //gosec:disable -- 错误忽略：测试清理
	if client.quicConfig.InitialStreamReceiveWindow != wantStream ||
		client.quicConfig.InitialConnectionReceiveWindow != wantConn {
		t.Fatalf("客户端窗口未接线：stream=%d conn=%d，期望 %d/%d",
			client.quicConfig.InitialStreamReceiveWindow,
			client.quicConfig.InitialConnectionReceiveWindow, wantStream, wantConn)
	}
}

// TestQuicWindowDefaultsLeaveTheLibraryInCharge 直接检查生产的默认值（newDefaultConfig
// 既被 config 注册使用也被本测试调用，不存在"测试里一套默认值、线上另一套"）。
// 默认必须是 0：0 才会让 quic-go 用它的 512KB 库内默认。若这里被改成任何非零值，
// 接线就等于替所有没配置过的用户缩小流控窗口。
func TestQuicWindowDefaultsLeaveTheLibraryInCharge(t *testing.T) {
	d := newDefaultConfig()
	if d.QUIC.InitialStreamWindow != 0 || d.QUIC.InitialConnWindow != 0 {
		t.Fatalf("默认初始窗口不是 0（stream=%d conn=%d）：接线后这会覆盖 quic-go 的 512KB 库内默认",
			d.QUIC.InitialStreamWindow, d.QUIC.InitialConnWindow)
	}
	// 0 一路传到 quic.Config，不在中间被填成别的数
	certPath, keyPath := writeSelfSigned(t, t.TempDir())
	server, err := NewServer(quicServerCtx(t, common.PickPort("udp", "127.0.0.1"), certPath, keyPath), nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close() //gosec:disable -- 错误忽略：测试清理
	if server.quicConfig.InitialStreamReceiveWindow != 0 || server.quicConfig.InitialConnectionReceiveWindow != 0 {
		t.Fatalf("0 被中间改写：stream=%d conn=%d",
			server.quicConfig.InitialStreamReceiveWindow, server.quicConfig.InitialConnectionReceiveWindow)
	}
}

func clientCtxWithWindows(t *testing.T, port int, stream, conn uint64) context.Context {
	t.Helper()
	cctx := config.WithConfig(context.Background(), Name, &Config{
		RemoteHost: "127.0.0.1",
		RemotePort: port,
		QUIC: QUICConfig{
			ALPN:                "hq-29",
			MaxIdleTimeout:      30,
			MaxIncomingStreams:  100,
			InitialStreamWindow: int(stream),
			InitialConnWindow:   int(conn),
			Congestion:          "bbr",
			Insecure:            true,
		},
	})
	return config.WithConfig(cctx, tlstunnel.Name, &tlstunnel.Config{
		TLS: tlstunnel.TLSConfig{Verify: false, SNI: "localhost"},
	})
}
