package http

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// CONNECT 分支把嗅探缓冲里的预读字节丢了：Peek(5) 触发 bufio.fill 一次读满
// 4096，若客户端把 CONNECT 请求与隧道首包（典型 TLS ClientHello）放在同一
// TCP 段，这些字节一并进入 reqBufReader；http.ReadRequest 解析完只消耗请求头，
// 残留字节留在 bufio。CONNECT 分支却把**裸 conn** 交给 ConnectConn，下游从
// conn 读，那些字节再也没有读者 —— 隧道首包静默丢失，握手超时。
// h2 分支早为同一问题加了 bufferedConn，这里漏了。
type prebufferConn struct {
	data   []byte
	off    int
	remote net.Addr
}

func newPrebufferConn(data string) *prebufferConn {
	return &prebufferConn{data: []byte(data), remote: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 443}}
}

// 一次性把全部预置字节交出去，模拟请求头与首包同段到达。
func (c *prebufferConn) Read(p []byte) (int, error) {
	if c.off >= len(c.data) {
		return 0, io.EOF
	}
	n := copy(p, c.data[c.off:])
	c.off += n
	return n, nil
}
func (c *prebufferConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *prebufferConn) Close() error                     { return nil }
func (c *prebufferConn) LocalAddr() net.Addr              { return c.remote }
func (c *prebufferConn) RemoteAddr() net.Addr             { return c.remote }
func (c *prebufferConn) SetDeadline(time.Time) error      { return nil }
func (c *prebufferConn) SetReadDeadline(time.Time) error  { return nil }
func (c *prebufferConn) SetWriteDeadline(time.Time) error { return nil }

func TestConnectPreservesPeekedPayload(t *testing.T) {
	const payload = "TLS-CLIENT-HELLO-IN-SAME-SEGMENT"
	req := "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"
	raw := newPrebufferConn(req + payload)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Server{
		underlay: &oneConnServer{conn: &pipeConn{Conn: raw}},
		ctx:      ctx,
		cancel:   cancel,
		connChan: make(chan tunnel.Conn, 4),
	}
	go s.acceptLoop()

	var cc tunnel.Conn
	select {
	case cc = <-s.connChan:
	case <-time.After(3 * time.Second):
		t.Fatal("handler 没有投递 ConnectConn，判据失效")
	}

	buf := make([]byte, len(payload))
	n, err := io.ReadFull(cc, buf)
	if err != nil || string(buf[:n]) != payload {
		t.Fatalf("隧道首包被吞：读到 %q（err=%v），期望 %q；Peek 预读字节留在 bufio 却交了裸 conn",
			buf[:n], err, payload)
	}
}
