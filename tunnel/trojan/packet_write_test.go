package trojan

import (
	"encoding/binary"
	"testing"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// TestWriteWithMetadataDoesNotRetainPayload 钉住 relay 那条跨层契约在 trojan UDP
// 帧这一侧：`WriteWithMetadata(p, m)` 返回之后，实现不许再保留、读取或别名 p。
// `proxy/proxy.go` 的包中继从池里取 buffer、写完立刻 Put，下一个包大概率拿到同一块
// 底层数组，所以"把 p 存起来晚点再发"不会当场出问题，而是把别人的负载串进这一帧。
// 这一层是"当场组帧 + 当场写"，判据用涂脏来证明帧里的负载是写入那一刻的字节。
func TestWriteWithMetadataDoesNotRetainPayload(t *testing.T) {
	rec := &recordConn{}
	c := &PacketConn{Conn: rec}

	m := &tunnel.Metadata{
		Command: Associate,
		Address: &tunnel.Address{AddressType: tunnel.IPv4, IP: []byte{1, 2, 3, 4}, Port: 53},
	}
	const original = "the original datagram"
	payload := []byte(original)

	if _, err := c.WriteWithMetadata(payload, m); err != nil {
		t.Fatalf("WriteWithMetadata: %v", err)
	}
	// 模拟 relay 的下一步：这块 buffer 归还池、被下一个包覆写
	for i := range payload {
		payload[i] = 0xAA
	}
	// 再发一帧，确认第一帧没有被第二帧的内容污染
	if _, err := c.WriteWithMetadata([]byte("second"), m); err != nil {
		t.Fatalf("第二帧: %v", err)
	}

	writes := rec.written()
	if len(writes) != 2 {
		t.Fatalf("落上线的帧数 = %d，期望 2", len(writes))
	}

	first := writes[0]
	// 帧 = ATYP(1) + IPv4(4) + 端口(2) + 长度(2) + CRLF(2) + 负载
	const headLen = 1 + 4 + 2 + 2 + 2
	if len(first) != headLen+len(original) {
		t.Fatalf("第一帧长度 %d，期望 %d", len(first), headLen+len(original))
	}
	if got := binary.BigEndian.Uint16(first[7:9]); int(got) != len(original) {
		t.Fatalf("帧里的长度字段 = %d，期望 %d", got, len(original))
	}
	if string(first[headLen:]) != original {
		t.Fatalf("第一帧的负载变成了 %q：relay 写完就复用那块 buffer，帧里必须存副本", first[headLen:])
	}
	if string(writes[1][headLen:]) != "second" {
		t.Fatalf("第二帧负载不对: %q", writes[1][headLen:])
	}
}
