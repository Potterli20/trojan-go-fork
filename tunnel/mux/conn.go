package mux

import (
	"io"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type stickyConn struct {
	tunnel.Conn
	synQueue chan []byte
	finQueue chan []byte
}

func (c *stickyConn) stickToPayload(p []byte) []byte {
	buf := make([]byte, 0, len(p)+16)
	for {
		select {
		case header := <-c.synQueue:
			buf = append(buf, header...)
		default:
			goto stick1
		}
	}
stick1:
	buf = append(buf, p...)
	for {
		select {
		case header := <-c.finQueue:
			buf = append(buf, header...)
		default:
			goto stick2
		}
	}
stick2:
	return buf
}

// closePaddingDeadline 限定 Close 时写出粘住的帧头与混淆 padding 的时限：
// 对端停止读取（已关闭/半开连接）时底层 Write 可能无限阻塞，若不设限时，
// mux Client 会在清理会话时永久挂起并拖死整个拨号路径。
const closePaddingDeadline = 10 * time.Second

func (c *stickyConn) Close() error {
	const maxPaddingLength = 512
	padding := [maxPaddingLength + 8]byte{'A', 'B', 'C', 'D', 'E', 'F'} // for debugging
	_ = c.Conn.SetWriteDeadline(time.Now().Add(closePaddingDeadline))
	buf := c.stickToPayload(nil)
	_, err := c.Write(append(buf, padding[:rand.IntN(maxPaddingLength)]...))
	if err != nil {
		log.Error("failed to write padding:", err)
	}
	_ = c.Conn.SetWriteDeadline(time.Time{})
	return c.Conn.Close()
}

func (c *stickyConn) Write(p []byte) (int, error) {
	if len(p) == 8 {
		if p[0] == 1 || p[0] == 2 { // smux 8 bytes header
			switch p[1] {
			// THE CONTENT OF THE BUFFER MIGHT CHANGE
			// NEVER STORE THE POINTER TO HEADER, COPY THE HEADER INSTEAD
			case 0:
				// cmdSYN
				header := make([]byte, 8)
				copy(header, p)
				c.synQueue <- header
				return 8, nil
			case 1:
				// cmdFIN
				header := make([]byte, 8)
				copy(header, p)
				c.finQueue <- header
				return 8, nil
			}
		} else {
			log.Debug("other 8 bytes header")
		}
	}
	_, err := c.Conn.Write(c.stickToPayload(p))
	return len(p), err
}

func newStickyConn(conn tunnel.Conn) *stickyConn {
	return &stickyConn{
		Conn:     conn,
		synQueue: make(chan []byte, 128),
		finQueue: make(chan []byte, 128),
	}
}

type Conn struct {
	rwc io.ReadWriteCloser
	tunnel.Conn
	lastActiveTime *atomic.Int64
	tracker        *log.ConnectionTracker
}

func (c *Conn) Read(p []byte) (int, error) {
	n, err := c.rwc.Read(p)
	if c.lastActiveTime != nil {
		c.lastActiveTime.Store(time.Now().UnixNano())
	}
	return n, err
}

func (c *Conn) Write(p []byte) (int, error) {
	n, err := c.rwc.Write(p)
	if c.lastActiveTime != nil {
		c.lastActiveTime.Store(time.Now().UnixNano())
	}
	return n, err
}

func (c *Conn) Close() error {
	if c.tracker != nil {
		c.tracker.Destroy("closed", 0, 0)
	}
	return c.rwc.Close()
}
