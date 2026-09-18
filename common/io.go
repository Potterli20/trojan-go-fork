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
