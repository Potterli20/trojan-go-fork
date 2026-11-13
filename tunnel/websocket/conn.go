package websocket

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"

	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// maxMessageSize 限制单条 WebSocket 消息的字节数,防止恶意超大帧耗尽内存
const maxMessageSize = 1 << 20

type OutboundConn struct {
	// Conn 是 websocket.NetConn 适配出的流式连接,
	// 消息边界由适配器抹平,Read/Write/deadline 均转发 coder 实现
	net.Conn
	tcpConn net.Conn
	request *http.Request // 握手请求,供上层读取真实 IP 相关 header
}

func (c *OutboundConn) Metadata() *tunnel.Metadata {
	return nil
}

// Request 返回 WebSocket 握手请求
func (c *OutboundConn) Request() *http.Request {
	return c.request
}

func (c *OutboundConn) RemoteAddr() net.Addr {
	if c.tcpConn != nil {
		return c.tcpConn.RemoteAddr()
	}
	return c.Conn.RemoteAddr()
}

func (c *OutboundConn) Close() error {
	// coder 发送 close 帧后同时关闭底层连接
	err := c.Conn.Close()
	if c.tcpConn != nil {
		if tcpErr := c.tcpConn.Close(); tcpErr != nil && err == nil {
			err = tcpErr
		}
	}
	return err
}

type InboundConn struct {
	OutboundConn
	cancel  context.CancelFunc // 取消后阻塞在 Read 上的 relay goroutine 被唤醒
	tracker *log.ConnectionTracker
	// 只给关停那行 Destroy 用。原先 Destroy 写死 0,0，日志恒为"零流量"，
	// 与 mux/quic 同型问题；这里自己数一遍，不去改 OutboundConn（它无 tracker）。
	sent atomic.Int64
	recv atomic.Int64
}

func (c *InboundConn) Read(p []byte) (int, error) {
	n, err := c.OutboundConn.Read(p)
	if n > 0 {
		c.recv.Add(int64(n))
	}
	return n, err
}

func (c *InboundConn) Write(p []byte) (int, error) {
	n, err := c.OutboundConn.Write(p)
	if n > 0 {
		c.sent.Add(int64(n))
	}
	return n, err
}

func (c *InboundConn) Close() error {
	c.cancel()
	if c.tracker != nil {
		c.tracker.Destroy("closed", uint64(c.sent.Load()), uint64(c.recv.Load()))
	}
	return c.OutboundConn.Close()
}
