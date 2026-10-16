package trojan

import (
	"testing"

	"github.com/Potterli20/trojan-go-fork/statistic/memory"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// testHeaderHash 只当占位用：本文件钉的是帧的字节顺序，不校 hash 的来源。
const testHeaderHash = "01234567890123456789012345678901234567890123456789012345"

func newHeaderConn(meta *tunnel.Metadata) (*OutboundConn, *recordConn) {
	rec := &recordConn{}
	return &OutboundConn{
		metadata: meta,
		user:     &memory.User{Hash: testHeaderHash},
		Conn:     rec,
	}, rec
}

// TestWriteHeaderWireFormat 钉住出站 TCP 头的线上字节。
// 这段从 `bytes.NewBuffer` + 丢弃错误的 `WriteTo` 改成按实际长度 append 之后，帧内容
// 必须一个字节都不变：hash + CRLF + CMD + ATYP + 地址 + 端口 + CRLF + payload。
// 期望值是把 trojan 规范直接手写成字节，不复用任何编码器——否则改坏编码器时判据会
// 跟着一起改，等于没钉住。顺带钉住"头与首个负载合成一次 Write"：分成两次写会在传输
// 层多出一个只含凭据的记录，分片语义与握手指纹都跟着变。
func TestWriteHeaderWireFormat(t *testing.T) {
	const payload = "ping"

	cases := []struct {
		name     string
		meta     *tunnel.Metadata
		preamble string
	}{
		{
			name: "IPv4",
			preamble: testHeaderHash + "\r\n" + string([]byte{
				0x01, // CMD = Connect
				0x01, // ATYP = IPv4
				127, 0, 0, 1,
				0x01, 0xbb, // 443
			}),
			meta: &tunnel.Metadata{
				Command: Connect,
				Address: &tunnel.Address{AddressType: tunnel.IPv4, IP: []byte{127, 0, 0, 1}, Port: 443},
			},
		},
		{
			name: "域名",
			preamble: testHeaderHash + "\r\n" + string([]byte{
				0x03, // CMD = Associate
				0x03, // ATYP = 域名
				11,   // 域名长度
			}) + "example.org" + string([]byte{0x00, 0x50}), // 80
			meta: &tunnel.Metadata{
				Command: Associate,
				Address: &tunnel.Address{AddressType: tunnel.DomainName, DomainName: "example.org", Port: 80},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newHeaderConn(tc.meta)

			ok, err := c.WriteHeader([]byte(payload))
			if err != nil {
				t.Fatalf("WriteHeader: %v", err)
			}
			if !ok {
				t.Fatal("首次写头应当报告已写出")
			}

			writes := rec.written()
			if len(writes) != 1 {
				t.Fatalf("头与首个负载被写成了 %d 次，必须是 1 次", len(writes))
			}
			want := tc.preamble + "\r\n" + payload
			if string(writes[0]) != want {
				t.Fatalf("线上字节不对：\n got %q\nwant %q", writes[0], want)
			}

			// 头已写出之后再写：不再重复帧头，也不该再往线上落任何由 WriteHeader 产生的字节
			ok, err = c.WriteHeader([]byte("second"))
			if err != nil {
				t.Fatalf("重复写头报错: %v", err)
			}
			if ok {
				t.Fatal("头已写出之后再次返回 written=true：帧头会被重复发进流里")
			}
			if got := rec.written(); len(got) != 1 {
				t.Fatalf("重复写头又往流里落了 %d 段：帧头被重发", len(got)-1)
			}
		})
	}
}

// TestWriteHeaderSerializeFailureWritesNothing 是 UDP 侧同型判据（packet_serialize_test.go）
// 在 TCP 头这一路的对应。
//
// 旧写法 `c.metadata.WriteTo(buf)` 丢弃返回值：地址编不进线上格式时缓冲区里只有
// hash + CRLF 那点残缺字节，CRLF + payload 照样发出，于是流里出现一个"没有地址的
// 请求头"，服务端按 trojan 状态机解就永久错位——而且是静默的，坏字节已经上线。
// 现在必须在落笔之前判死，并且这条连接不许再被复用（headerFailed）。
func TestWriteHeaderSerializeFailureWritesNothing(t *testing.T) {
	cases := []struct {
		name    string
		address *tunnel.Address
	}{
		{"nil Address", nil},
		{"非法 ATYP", &tunnel.Address{AddressType: tunnel.AddressType(99), DomainName: "example.org", Port: 443}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newHeaderConn(&tunnel.Metadata{Command: Connect, Address: tc.address})

			written, err := c.WriteHeader([]byte("payload"))
			if err == nil {
				t.Fatal("序列化失败必须返回错误")
			}
			if written {
				t.Fatal("序列化失败却报告已写出")
			}
			if got := rec.written(); len(got) != 0 {
				t.Fatalf("序列化失败仍写出字节 %q：残缺头一旦上线，服务端的状态机就永久错位", got[0])
			}

			// 连接已判死：第二次写既不再尝试发头，也不往流里落任何字节
			written, err = c.WriteHeader([]byte("again"))
			if err == nil || written {
				t.Fatalf("判死之后的第二次写没有被拒（written=%v err=%v）", written, err)
			}
			if got := rec.written(); len(got) != 0 {
				t.Fatalf("判死之后仍然写出了字节 %q", got[0])
			}
		})
	}
}
