package adapter

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// TestSilentConnectionDoesNotStallAccept 盯住嗅探的并发口径：Accept 是串行的，
// 若把"等对端发首字节"的 Read 写在循环体里，一条只连接不发数据的对端就能让整个
// socks/http 入口停止接纳新连接，最长 firstByteTimeout(10s)。
//
// 判据：先挂一条静默连接（确保它已进入嗅探并卡在 Read 上），再发一条完整的
// SOCKS5 握手——它必须在有限时间内被接纳并投递出来。
func TestSilentConnectionDoesNotStallAccept(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		tcpListener: ln,
		socksConn:   make(chan tunnel.Conn, 4),
		httpConn:    make(chan tunnel.Conn, 4),
		ctx:         ctx,
		cancel:      cancel,
	}
	s.socksLock.Lock()
	s.nextSocks = true
	s.socksLock.Unlock()

	s.wg.Go(func() { s.acceptConnLoop() })
	t.Cleanup(func() {
		cancel()
		ln.Close()
		s.wg.Wait()
	})

	silent, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial silent: %v", err)
	}
	t.Cleanup(func() { silent.Close() })
	// 让 accept 循环先把这条连接接走并卡在嗅探读上
	time.Sleep(100 * time.Millisecond)

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial client: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	if _, err := client.Write([]byte{5, 1, 0}); err != nil {
		t.Fatalf("write socks greeting: %v", err)
	}

	select {
	case c := <-s.socksConn:
		t.Cleanup(func() { c.Close() })
		if _, err := c.Write([]byte{5, 0}); err != nil {
			t.Fatalf("write socks response: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("一条静默连接把入站 accept 卡住了：第二条 SOCKS 连接没有被接纳")
	}

	client.SetReadDeadline(time.Now().Add(time.Second))
	resp := make([]byte, 2)
	if _, err := io.ReadFull(client, resp); err != nil {
		t.Fatalf("read socks response: %v", err)
	}
	if resp[0] != 5 || resp[1] != 0 {
		t.Fatalf("响应不对: %v", resp)
	}
}
