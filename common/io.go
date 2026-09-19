package common

import (
	"io"
	"net"
	"sync"

	"github.com/Potterli20/trojan-go-fork/log"
)

type RewindReader struct {
	mu         sync.Mutex
	rawReader  io.Reader
	buf        []byte
	bufReadIdx int
	rewound    bool
	buffering  bool
	bufferSize int
}

// maxRewindBufferSize 是嗅探缓冲的绝对上限（与调用方 SetBufferSize 传入的
// 预分配提示无关）。bufferSize 只是预分配/嗅探提示，合法 TLS 握手在 buffering
// 期间会读入远大于它的 ClientHello，绝不能按它截断，否则回放不完整会破坏握手。
// 这里用独立的硬上限约束累积量：防止未认证对端在嗅探完成前把 r.buf 撑到数百 MB
// （最危险路径 tunnel/transport/server.go 明文 http.ReadRequest，Go 1.27 已无
// header 大小上限）。64KB 远大于任何合法协议握手首包，只有异常/攻击流量才会触及，
// 一旦超过即停止累积转直通——此时嗅探必然失败，但单连接常驻内存有界。
const maxRewindBufferSize = 64 * 1024

// MaxSniffRequestBytes 限制嗅探阶段解析单个 HTTP 请求时读取的字节数。
// http.ReadRequest 走的是客户端解析路径，Go 1.21 之后 header 上限是 math.MaxInt64，
// 未认证对端只靠请求头就能让单连接分配数百 MB；maxRewindBufferSize 只管住
// RewindReader 自己的累积量，管不住解析侧的分配，所以读取侧还要独立设限。
// 32KB 覆盖带长 cookie/URL 的真实请求，同时小于 maxRewindBufferSize，
// 保证被读走的字节仍完整可回放。
const MaxSniffRequestBytes = 32 * 1024

// BoundedReader 给一个长期存活的 reader 临时设读取上限，嗅探结束后必须调用
// Unlimited 交还。用于 bufio 会被后续协议复用、不能一次性截断的场景
// （tunnel/websocket 的升级请求解析）。嗅探完即丢弃的 reader 直接用 io.LimitReader。
type BoundedReader struct {
	raw       io.Reader
	remaining int64 // < 0 表示不限制
}

func NewBoundedReader(raw io.Reader) *BoundedReader {
	return &BoundedReader{raw: raw, remaining: -1}
}

func (b *BoundedReader) Limit(n int64) {
	b.remaining = n
}

func (b *BoundedReader) Unlimited() {
	b.remaining = -1
}

func (b *BoundedReader) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	if b.remaining > 0 && int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.raw.Read(p)
	if b.remaining > 0 {
		b.remaining -= int64(n)
	}
	return n, err
}

func (r *RewindReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	if r.rewound {
		if len(r.buf) > r.bufReadIdx {
			n := copy(p, r.buf[r.bufReadIdx:])
			r.bufReadIdx += n
			r.mu.Unlock()
			return n, nil
		}
		r.rewound = false
		if !r.buffering {
			// 回放完毕且不再缓冲：立即释放嗅探期间的缓冲。否则最多 64KB 会一直
			// 挂在这条连接的生命周期上，一条连接叠几层 RewindConn 就是几百 KB。
			r.buf = nil
			r.bufReadIdx = 0
		}
	}
	buffering := r.buffering
	r.mu.Unlock()

	n, err := r.rawReader.Read(p)

	if buffering {
		r.mu.Lock()
		if len(r.buf)+n <= maxRewindBufferSize {
			r.buf = append(r.buf, p[:n]...)
		} else {
			// 超出绝对上限：放弃缓冲转直通，不再累积，保证单连接内存有界。
			r.buffering = false
			log.Debug("rewind buffer exceeded hard limit, buffering disabled")
		}
		r.mu.Unlock()
	}
	return n, err
}

func (r *RewindReader) ReadByte() (byte, error) {
	buf := [1]byte{}
	_, err := r.Read(buf[:])
	return buf[0], err
}

func (r *RewindReader) Discard(n int) (int, error) {
	buf := [128]byte{}
	if n < 128 {
		return r.Read(buf[:n])
	}
	discarded := 0
	for range n / 128 {
		_, err := r.Read(buf[:])
		if err != nil {
			return discarded, err
		}
		discarded += 128
	}
	if rest := n % 128; rest != 0 {
		return r.Read(buf[:rest])
	}
	return n, nil
}

func (r *RewindReader) Rewind() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bufferSize == 0 {
		log.Error(NewError("attempting to rewind without buffer"))
		return
	}
	r.rewound = true
	r.bufReadIdx = 0
}

func (r *RewindReader) StopBuffering() {
	r.mu.Lock()
	r.buffering = false
	if !r.rewound {
		r.buf = nil
		r.bufReadIdx = 0
	}
	r.mu.Unlock()
}

func (r *RewindReader) SetBufferSize(size int) {
	r.mu.Lock()
	if size == 0 { // disable buffering
		if !r.buffering {
			panic("reader is disabled")
		}
		r.buffering = false
		r.buf = nil
		r.bufReadIdx = 0
		r.bufferSize = 0
	} else {
		if r.buffering {
			panic("reader is buffering")
		}
		r.buffering = true
		r.bufReadIdx = 0
		r.bufferSize = size
		r.buf = make([]byte, 0, size)
	}
	r.mu.Unlock()
}

type RewindConn struct {
	net.Conn
	*RewindReader
}

func (c *RewindConn) Read(p []byte) (int, error) {
	return c.RewindReader.Read(p)
}

func (c *RewindConn) Close() error {
	c.RewindReader.StopBuffering()
	return c.Conn.Close()
}

func NewRewindConn(conn net.Conn) *RewindConn {
	return &RewindConn{
		Conn: conn,
		RewindReader: &RewindReader{
			rawReader: conn,
		},
	}
}

type StickyWriter struct {
	rawWriter   io.Writer
	writeBuffer []byte
	MaxBuffered int
}

func (w *StickyWriter) Write(p []byte) (int, error) {
	if w.MaxBuffered > 0 {
		w.MaxBuffered--
		w.writeBuffer = append(w.writeBuffer, p...)
		if w.MaxBuffered != 0 {
			return len(p), nil
		}
		w.MaxBuffered = 0
		_, err := w.rawWriter.Write(w.writeBuffer)
		w.writeBuffer = nil
		return len(p), err
	}
	return w.rawWriter.Write(p)
}
