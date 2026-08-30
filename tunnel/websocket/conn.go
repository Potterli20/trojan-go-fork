package websocket

import (
	"context"
	"net"
	"net/http"

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
}

func (c *InboundConn) Close() error {
	c.cancel()
	if c.tracker != nil {
		c.tracker.Destroy("closed", 0, 0)
	}
	return c.OutboundConn.Close()
}
