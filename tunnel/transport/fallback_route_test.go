package transport

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

const junkPayload = "this is not an http request\r\n\r\n"

func newRoutingServer(t *testing.T) *Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := &Server{
		connChan: make(chan tunnel.Conn, 4),
		wsChan:   make(chan tunnel.Conn, 4),
		ctx:      ctx,
		cancel:   cancel,
	}
	s.httpLock.Lock()
	s.nextHTTP = true // 已有 websocket 下游登记
	s.httpLock.Unlock()
	return s
}

func driveJunk(t *testing.T, s *Server) <-chan struct{} {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close() })
	done := make(chan struct{})
	go func() {
		s.handleConnection(server)
		close(done)
	}()
	if _, err := client.Write([]byte(junkPayload)); err != nil {
		t.Fatalf("write junk: %v", err)
	}
	return done
}

// TestNonHTTPFallsBackToWebsocketWhenNoDirectConsumer 钉住明文插件 + websocket 栈
// （`transport → websocket → trojan`）的路由：这种栈里 connChan **没有消费者**，
// 嗅探失败的非 HTTP 连接必须交给 wsChan（websocket 会重放字节并重定向到诱饵）。
// 旧实现一律投 connChan，缓冲 32 条填满后每个新连接都卡在发送上，扫描流量即可
// 把协程与 fd 堆到关停为止。
func TestNonHTTPFallsBackToWebsocketWhenNoDirectConsumer(t *testing.T) {
	s := newRoutingServer(t)
	done := driveJunk(t, s)

	select {
	case c := <-s.wsChan:
		c.Close() //gosec:disable -- 错误忽略：测试清理
	case <-time.After(3 * time.Second):
		t.Fatal("非 HTTP 连接没有交给 websocket fallback：它被投进了无人消费的 connChan")
	}
	select {
	case c := <-s.connChan:
		c.Close() //gosec:disable -- 错误忽略：测试清理
		t.Fatal("没有直接消费者时仍把连接投进了 connChan")
	default:
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handleConnection 没有返回")
	}
}

// TestNonHTTPGoesToDirectConsumerWhenPresent 是对照：custom 栈里 transport 同时挂了
// tls/trojan（directConsumer=true）时，非 HTTP 字节仍应交给 connChan 继续解析。
func TestNonHTTPGoesToDirectConsumerWhenPresent(t *testing.T) {
	s := newRoutingServer(t)
	s.httpLock.Lock()
	s.directConsumer = true
	s.httpLock.Unlock()
	done := driveJunk(t, s)

	select {
	case c := <-s.connChan:
		c.Close() //gosec:disable -- 错误忽略：测试清理
	case <-time.After(3 * time.Second):
		t.Fatal("有直接消费者时，非 HTTP 连接没有交给 connChan")
	}
	select {
	case c := <-s.wsChan:
		c.Close() //gosec:disable -- 错误忽略：测试清理
		t.Fatal("有直接消费者时仍把非 HTTP 连接塞给了 websocket")
	default:
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handleConnection 没有返回")
	}
}
