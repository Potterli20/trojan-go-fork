package mux

import (
	"io"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type stickyConn struct {
	tunnel.Conn
	// writeMu 串行化 Write 与 Close 的 padding 写:同一 stickyConn 可能被
	// 多个调用方并发 Close(并发 DialConn 的 OpenStream 失败清理、cleanLoop、
	// Client.Close),而底层 AEAD(trojan/shadowsocks)的 Write 无锁,
	// 并发写会破坏加密状态(nonce 计数器),-race 下报 DATA RACE
	writeMu   sync.Mutex
	closeOnce sync.Once
	synQueue  chan []byte
	finQueue  chan []byte
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
	var err error
	// 并发 Close 只执行一次 padding 写;SetWriteDeadline 也必须与数据写互斥,
	// 否则会把 10s 截止时间强加给并发数据写
	c.closeOnce.Do(func() {
		const maxPaddingLength = 512
		padding := [maxPaddingLength + 8]byte{'A', 'B', 'C', 'D', 'E', 'F'} // for debugging
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		_ = c.Conn.SetWriteDeadline(time.Now().Add(closePaddingDeadline))
		// 与原实现一致经两次 stickToPayload drain(原先经 c.Write 间接完成);
		// 此处不能调 c.Write——writeMu 不可重入
		payload := c.stickToPayload(append(c.stickToPayload(nil), padding[:rand.IntN(maxPaddingLength)]...))
		_, err = c.Conn.Write(payload)
		if err != nil {
			log.Error("failed to write padding:", err)
		}
		_ = c.Conn.SetWriteDeadline(time.Time{})
		err = c.Conn.Close()
	})
	return err
}

func (c *stickyConn) Write(p []byte) (int, error) {
	// smux 写循环是唯一的数据写者(单 goroutine),但 Close 的 padding 写
	// 与之并发,底层 AEAD 无锁,必须互斥
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
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
