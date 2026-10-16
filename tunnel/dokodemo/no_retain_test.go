package dokodemo

import (
	"context"
	"net"
	"testing"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// TestWriteWithMetadataDoesNotRetainPayload 钉住 relay 那条跨层契约：
// `WriteWithMetadata(p, m)` 返回之后，实现不许再保留、读取或别名 p。
// `proxy/proxy.go` 的包中继从池里取 buffer，写完立刻 Put 回去，下一个包大概率拿到
// 同一块底层数组——"入队存引用"在这里不会当场炸，而是把别人的 UDP 负载串进来。
// 同型判据另见 tunnel/socks/no_retain_test.go 与 tunnel/trojan/packet_write_test.go。
func TestWriteWithMetadataDoesNotRetainPayload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fixed := &tunnel.Address{AddressType: tunnel.IPv4, IP: []byte{127, 0, 0, 1}, Port: 8080}
	c := &PacketConn{
		input:    make(chan []byte, 16),
		output:   make(chan []byte, 16),
		metadata: &tunnel.Metadata{Command: tunnel.Command(1), Address: fixed},
		src:      &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234},
		ctx:      ctx,
		cancel:   cancel,
	}

	const original = "the original datagram"
	payload := []byte(original)
	m := &tunnel.Metadata{Command: tunnel.Command(1), Address: fixed}
	n, err := c.WriteWithMetadata(payload, m)
	if err != nil {
		t.Fatalf("WriteWithMetadata: %v", err)
	}
	if n != len(original) {
		t.Fatalf("返回写入长度 %d，期望 %d", n, len(original))
	}

	// 模拟 relay 的下一步：这块 buffer 归还池、被下一个包覆写
	for i := range payload {
		payload[i] = 0xAA
	}

	got := <-c.output
	if string(got) != original {
		t.Fatalf("入队的负载变成了 %q：这一层必须在入队前复制，relay 写完就复用那块 buffer", got)
	}
}
