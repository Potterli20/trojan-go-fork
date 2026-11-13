package transport

import (
	"bufio"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// 嗅探阶段解析的是未认证对端可控字节，而 handleConnection 跑在独立 goroutine 里：
// 没有父层 accept 循环的 guard，一次 panic 会带走整个进程。注入 panic 后必须被
// 收在嗅探步内，连接按"非 HTTP"路径交给 websocket fallback。
func TestSniffPanicIsContained(t *testing.T) {
	ctx := t.Context()
	s := &Server{
		ctx:      ctx,
		connChan: make(chan tunnel.Conn, 1),
		wsChan:   make(chan tunnel.Conn, 1),
		nextHTTP: true,
	}

	peer, raw := net.Pipe()
	defer peer.Close() //gosec:disable -- 错误忽略：测试清理

	orig := readSniffRequest
	t.Cleanup(func() { readSniffRequest = orig })
	readSniffRequest = func(*bufio.Reader) (*http.Request, error) {
		panic("injected: panic while parsing a peer-controlled http request")
	}

	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		s.handleConnection(raw)
	}()
	select {
	case p := <-done:
		if p != nil {
			t.Fatalf("嗅探解析的 panic 穿出了 handleConnection，整个进程会被带走：%v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handleConnection 未返回")
	}

	select {
	case conn := <-s.wsChan:
		conn.Close() //gosec:disable -- 错误忽略：测试清理
	case <-time.After(time.Second):
		t.Fatal("panic 之后连接没有按非 HTTP 路径交给 websocket fallback")
	}
}
