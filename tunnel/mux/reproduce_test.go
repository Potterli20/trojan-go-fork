package mux

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtaci/smux"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// pipeTunnelConn 将 TCP 连接适配为 tunnel.Conn
type pipeTunnelConn struct {
	net.Conn
}

func (c *pipeTunnelConn) Metadata() *tunnel.Metadata {
	return &tunnel.Metadata{}
}

// TestStickyConnConcurrentSessions 回归测试：并发建立大量独立 mux session
// 并立即完成一次往返（对应 100 个 socks 连接同时拨号的场景）。
// stickyConn 扣留 SYN/FIN 帧并粘连到后续 payload，配合 smux shaper 的
// 控制帧优先调度，要求 SYN 始终先于同 stream 的数据帧到达服务端、
// FIN 不阻塞会话关闭；Close 时的 padding 写限时（closePaddingDeadline）
// 保证对端停止读取时 mux Client 的清理路径不会被无限挂起。
func TestStickyConnConcurrentSessions(t *testing.T) {
	const numSessions = 100

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	acceptCh := make(chan net.Conn, numSessions)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			acceptCh <- conn
		}
	}()

	var wgServer sync.WaitGroup
	serverErrs := make(chan error, numSessions)
	clientSessions := make([]*smux.Session, numSessions)
	closes := make([]func(), numSessions)

	for i := range numSessions {
		c1, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		c2 := <-acceptCh
		clientConn := newStickyConn(&pipeTunnelConn{Conn: c1})
		serverConn := &pipeTunnelConn{Conn: c2}

		serverSession, err := smux.Server(serverConn, smux.DefaultConfig())
		if err != nil {
			t.Fatalf("server %d: %v", i, err)
		}
		clientSession, err := smux.Client(clientConn, smux.DefaultConfig())
		if err != nil {
			t.Fatalf("client %d: %v", i, err)
		}
		clientSessions[i] = clientSession
		closes[i] = func() {
			clientSession.Close()
			serverSession.Close()
			c1.Close()
			c2.Close()
		}

		wgServer.Go(func() {
			stream, err := serverSession.AcceptStream()
			if err != nil {
				serverErrs <- err
				return
			}
			buf := make([]byte, 1024)
			n, _ := stream.Read(buf)
			stream.Write(buf[:n])
			stream.Close()
		})
	}

	var wg sync.WaitGroup
	for i := range numSessions {
		wg.Go(func() {
			stream, err := clientSessions[i].OpenStream()
			if err != nil {
				t.Logf("open stream %d failed: %v", i, err)
				return
			}
			payload := make([]byte, 1024)
			for j := range payload {
				payload[j] = byte(i)
			}
			if _, err := stream.Write(payload); err != nil {
				t.Logf("write %d failed: %v", i, err)
				return
			}
			buf := make([]byte, 1024)
			if _, err := stream.Read(buf); err != nil {
				t.Logf("read %d failed: %v", i, err)
				return
			}
			stream.Close()
		})
	}

	serverDone := make(chan struct{})
	go func() {
		wgServer.Wait()
		close(serverDone)
	}()
	clientDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(clientDone)
	}()

	select {
	case err := <-serverErrs:
		t.Fatal(err)
	case <-serverDone:
	case <-time.After(30 * time.Second):
		t.Fatalf("timeout: server side stuck, %d sessions accepted", numSessions-len(serverErrs))
	}
	select {
	case <-clientDone:
	case <-time.After(30 * time.Second):
		t.Fatal("timeout: client side stuck")
	}

	for _, f := range closes {
		f()
	}
}

// trackedTunnelConn 记录 Close 是否被调用，用来断言会话真的被回收
type trackedTunnelConn struct {
	net.Conn
	closed atomic.Bool
}

func (c *trackedTunnelConn) Metadata() *tunnel.Metadata { return &tunnel.Metadata{} }

func (c *trackedTunnelConn) Close() error {
	c.closed.Store(true)
	return c.Conn.Close()
}

// blockingUnderlay 的 DialConn 停在 release 上，让测试能把「Close 已排空池」与
// 「拨号刚返回」这两件事的先后顺序固定下来，复现原本需要运气才撞得出的交错
type blockingUnderlay struct {
	entered chan struct{}
	release chan struct{}
	conn    tunnel.Conn
	once    sync.Once
}

func (u *blockingUnderlay) DialConn(*tunnel.Address, tunnel.Tunnel) (tunnel.Conn, error) {
	u.once.Do(func() { close(u.entered) })
	<-u.release
	return u.conn, nil
}

func (u *blockingUnderlay) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, common.NewError("not supported")
}

func (u *blockingUnderlay) Close() error { return nil }

// TestMuxDialDuringCloseDoesNotStrandSession 回归：newMuxClient 在锁外拨号，
// 回锁后若不检查 ctx，这条会话就会插进一个已被 Close（或 cleanLoop 的 ctx.Done 分支）
// 排空过的池里。此后没有任何东西会再回收它：stickyConn 的 fd 与 smux 的
// readLoop/writeLoop 永久残留，每撞一次漏一次。
func TestMuxDialDuringCloseDoesNotStrandSession(t *testing.T) {
	muxSide, farSide := net.Pipe()
	// 对端必须持续收取，否则 smux 的写循环和 Close 的 padding 写都会阻塞在 net.Pipe 上
	go io.Copy(io.Discard, farSide) //gosec:disable -- 错误忽略：测试用排空通道
	defer farSide.Close()           //gosec:disable -- 错误忽略：测试清理
	tracked := &trackedTunnelConn{Conn: muxSide}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = config.WithConfig(ctx, Name, &Config{
		Mux: MuxConfig{Enabled: true, IdleTimeout: 30, Concurrency: 8},
	})
	underlay := &blockingUnderlay{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		conn:    tracked,
	}
	client, err := NewClient(ctx, underlay)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	dialErr := make(chan error, 1)
	go func() {
		_, err := client.DialConn(&tunnel.Address{DomainName: "MUX_CONN", AddressType: tunnel.DomainName}, &Tunnel{})
		dialErr <- err
	}()

	// 先确认拨号已经进去（必然还没返回），再关停：这样池的排空一定发生在拨号之前
	<-underlay.entered
	if err := client.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	close(underlay.release)

	select {
	case err := <-dialErr:
		if err == nil {
			t.Fatal("关停期间的 DialConn 竟然成功：会话被插进已排空的池，fd 与 goroutine 永久残留")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DialConn 没有返回")
	}

	if !tracked.closed.Load() {
		t.Fatal("竞态窗口里建立的会话没有被 Close：underlayConn 的 fd 仍在泄漏")
	}
}
