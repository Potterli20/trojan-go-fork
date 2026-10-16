package socks

import (
	"context"
	"net"
	"testing"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// TestWriteWithMetadataDoesNotRetainPayload 钉住 relay 那条跨层契约：
// `WriteWithMetadata(p, m)` 返回之后，实现不许再保留、读取或别名 p。
// `proxy/proxy.go` 的包中继用的是池里的 buffer，写完**立刻** Put 回去，下一个包
// Get 到同一块底层的概率极高——所以"入队时存指针"这种写法不会立刻出问题，而是
// 在包与包之间串数据，且串的是别人的 UDP 负载。这一层是"复制后入队"，判据就用
// 涂脏来证明复制真的发生了。
func TestWriteWithMetadataDoesNotRetainPayload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &PacketConn{
		input:  make(chan *packetInfo, udpSessionQueueSize),
		output: make(chan *packetInfo, udpSessionQueueSize),
		ctx:    ctx,
		cancel: cancel,
		src:    &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234},
	}
	meta := &tunnel.Metadata{
		Command: tunnel.Command(1),
		Address: &tunnel.Address{AddressType: tunnel.IPv4, IP: []byte{1, 2, 3, 4}, Port: 53},
	}

	const original = "the original datagram"
	payload := []byte(original)
	n, err := c.WriteWithMetadata(payload, meta)
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

	info := <-c.output
	if string(info.payload) != original {
		t.Fatalf("入队的负载变成了 %q：这一层必须在入队前复制，relay 写完就复用那块 buffer", info.payload)
	}
	if &info.payload[0] == &payload[0] {
		t.Fatal("入队的切片与调用方的 buffer 同一块底层数组")
	}
}
