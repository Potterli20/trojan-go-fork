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
	// releaseHandshakeBufs 把手握期的 bufio 读写缓冲归还到池。存活期是整条会话
	// （coder 继续用 Hijack 拿到的 ReadWriter 读帧），所以归还点在这里；
	// sync.OnceFunc 保证重复 Close 只归还一次。
	releaseHandshakeBufs func()
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
	// 必须先关 websocket 连接再归还缓冲：coder 的 Close 会写 close 帧并 flush，
	// 用的正是 Hijack 拿到的那对 ReadWriter；先归还（Reset(nil)）会让它当场空指针。
	err := c.OutboundConn.Close()
	if c.releaseHandshakeBufs != nil {
		c.releaseHandshakeBufs()
	}
	if c.tracker != nil {
		c.tracker.Destroy("closed", uint64(c.sent.Load()), uint64(c.recv.Load()))
	}
	return err
}
