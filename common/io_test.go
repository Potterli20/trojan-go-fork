package common

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"

	"github.com/xtls/xray-core/common"
)

func TestBufferedReader(t *testing.T) {
	payload := [1024]byte{}
	rand.Reader.Read(payload[:])
	rawReader := bytes.NewBuffer(payload[:])
	r := RewindReader{
		rawReader: rawReader,
	}
	r.SetBufferSize(2048)
	buf1 := make([]byte, 512)
	buf2 := make([]byte, 512)
	common.Must2(r.Read(buf1))
	r.Rewind()
	common.Must2(r.Read(buf2))
	if !bytes.Equal(buf1, buf2) {
		t.Fail()
	}
	buf3 := make([]byte, 512)
	common.Must2(r.Read(buf3))
	if !bytes.Equal(buf3, payload[512:]) {
		t.Fail()
	}
	r.Rewind()
	buf4 := make([]byte, 1024)
	common.Must2(r.Read(buf4))
	if !bytes.Equal(payload[:], buf4) {
		t.Fail()
	}
}

// TestRewindReaderBufferBounded 验证嗅探缓冲不会无界增长:即使未认证对端在
// 嗅探完成前持续灌入数据,r.buf 也不会超过 maxRewindBufferSize。
// 对应 tunnel/transport/server.go 明文 http.ReadRequest 的 pre-auth 放大路径
// (Go 1.27 的 http.ReadRequest 已无 header 上限)。
func TestRewindReaderBufferBounded(t *testing.T) {
	// 构造 1MB 源,远大于绝对上限
	source := make([]byte, 1024*1024)
	rand.Reader.Read(source)
	r := RewindReader{
		rawReader: bytes.NewBuffer(source),
	}
	// bufferSize 只是提示值(很小),不应成为累积上限
	r.SetBufferSize(16)

	buf := make([]byte, 4096)
	for {
		if _, err := r.Read(buf); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("unexpected read error: %v", err)
		}
		// 累积量必须始终有界
		if len(r.buf) > maxRewindBufferSize {
			t.Fatalf("rewind buffer exceeded hard limit: len=%d > %d", len(r.buf), maxRewindBufferSize)
		}
		// 越界后应自动关闭 buffering
		if !r.buffering {
			break
		}
	}

	if len(r.buf) > maxRewindBufferSize {
		t.Fatalf("final buffer too large: len=%d > %d", len(r.buf), maxRewindBufferSize)
	}
	if r.buffering {
		t.Fatalf("expected buffering to be disabled after exceeding hard limit")
	}
}

// 回放结束且已 StopBuffering 之后，嗅探期间累积的缓冲必须立刻释放，
// 否则最多 64KB 会挂在整条连接的生命周期上（一条连接还会叠多层 RewindConn）
func TestRewindReaderReleasesBufferAfterReplay(t *testing.T) {
	source := make([]byte, 1024)
	rand.Reader.Read(source)
	r := RewindReader{rawReader: bytes.NewBuffer(source)}
	r.SetBufferSize(512)

	first := make([]byte, 512)
	common.Must2(r.Read(first))
	r.Rewind()
	r.StopBuffering()

	replayed := make([]byte, 512)
	common.Must2(r.Read(replayed))
	if !bytes.Equal(first, replayed) {
		t.Fatal("replayed data differs from the sniffed data")
	}
	if r.buf == nil {
		t.Fatal("buffer must survive while replay is still pending")
	}

	tail := make([]byte, 16)
	common.Must2(r.Read(tail))
	if !bytes.Equal(tail, source[512:528]) {
		t.Fatal("data after replay is corrupted")
	}
	if r.buf != nil {
		t.Fatalf("buffer not released after replay finished: len=%d cap=%d", len(r.buf), cap(r.buf))
	}
}

func TestBoundedReader(t *testing.T) {
	source := make([]byte, 64)
	rand.Reader.Read(source)
	b := NewBoundedReader(bytes.NewBuffer(source))

	buf := make([]byte, 32)
	n, err := b.Read(buf)
	if n != 32 || err != nil {
		t.Fatalf("unlimited read failed: n=%d err=%v", n, err)
	}

	b.Limit(8)
	limited := make([]byte, 0, 32)
	rest := make([]byte, 32)
	for {
		n, err := b.Read(rest)
		limited = append(limited, rest[:n]...)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if !bytes.Equal(limited, source[32:40]) {
		t.Fatalf("limit not enforced: got %d bytes, want 8", len(limited))
	}

	// 解除限制后必须能继续读原始流（websocket 升级后复用同一个 bufio 的前提）
	b.Unlimited()
	if n, err := b.Read(rest); n == 0 || err != nil {
		t.Fatalf("read after Unlimited failed: n=%d err=%v", n, err)
	}
}
