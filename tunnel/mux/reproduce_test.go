package mux

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/xtaci/smux"

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

		wgServer.Add(1)
		go func(i int) {
			defer wgServer.Done()
			stream, err := serverSession.AcceptStream()
			if err != nil {
				serverErrs <- err
				return
			}
			buf := make([]byte, 1024)
			n, _ := stream.Read(buf)
			stream.Write(buf[:n])
			stream.Close()
		}(i)
	}

	var wg sync.WaitGroup
	for i := range numSessions {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
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
		}(i)
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
