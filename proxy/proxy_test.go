package proxy

import (
	"context"
	"testing"

	"github.com/Potterli20/trojan-go-fork/config"
)

func TestBoundedBufPoolReuseAndResidencyLimit(t *testing.T) {
	pool := newBoundedBufPool(128, 2)

	buf := pool.Get()
	if len(buf) != 128 || cap(buf) != 128 {
		t.Fatalf("Get returned buffer len=%d cap=%d, want 128", len(buf), cap(buf))
	}
	pool.Put(buf)
	if n := len(pool.bufs); n != 1 {
		t.Fatalf("pool holds %d buffers after Put, want 1", n)
	}

	reused := pool.Get()
	if &reused[0] != &buf[0] {
		t.Fatal("Get did not reuse the pooled buffer")
	}
	if len(reused) != 128 {
		t.Fatalf("reused buffer has len=%d, want 128", len(reused))
	}

	// 驻留上限：超出的 Put 直接丢弃，不阻塞
	first, second, third := pool.Get(), pool.Get(), pool.Get()
	pool.Put(first)
	pool.Put(second)
	pool.Put(third)
	if n := len(pool.bufs); n != 2 {
		t.Fatalf("pool holds %d buffers, want limit 2", n)
	}

	// 尺寸不符的 buffer 不能进池，否则会被别的转发路径按错误长度使用
	pool.Put(make([]byte, 64, 64))
	if n := len(pool.bufs); n != 2 {
		t.Fatalf("wrong-capacity buffer was pooled: %d", n)
	}
}

func TestBoundedBufPoolDisabled(t *testing.T) {
	pool := newBoundedBufPool(128, 0)
	pool.Put(pool.Get())
	if n := len(pool.bufs); n != 0 {
		t.Fatalf("limit<=0 must disable pooling, pool holds %d", n)
	}
	if len(pool.Get()) != 128 {
		t.Fatal("Get must still return a usable buffer when pooling is disabled")
	}
}

func TestNewProxyPacketPoolCoversMaxPacketSize(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 默认配置：转发 buffer 已经容得下一个 UDP 包，两个路径共用一个池
	p := NewProxy(ctx, cancel, nil, nil)
	if p.bufPool.size != defaultBufSize {
		t.Fatalf("default buffer size = %d, want %d", p.bufPool.size, defaultBufSize)
	}
	if p.packetPool != p.bufPool {
		t.Fatal("packet pool should be shared when the relay buffer is large enough")
	}

	// 调小 relay_buffer_size 时，UDP 路径仍必须拿到能装下整包的 buffer：
	// trojan 的 ReadWithMetadata 在 buffer 小于包长时返回错误，会断开整条 packet 流
	ctx = config.WithConfig(ctx, Name, &Config{
		RelayBufferSize:  1024,
		RelayBufferCount: 16,
	})
	p = NewProxy(ctx, cancel, nil, nil)
	if p.bufPool.size != 1024 {
		t.Fatalf("relay buffer size = %d, want 1024", p.bufPool.size)
	}
	if p.packetPool == p.bufPool {
		t.Fatal("packet pool must not reuse an undersized relay buffer")
	}
	if p.packetPool.size < MaxPacketSize {
		t.Fatalf("packet buffer size = %d, want at least %d", p.packetPool.size, MaxPacketSize)
	}
	if p.packetPool.limit != 16 {
		t.Fatalf("packet pool limit = %d, want 16", p.packetPool.limit)
	}
}
