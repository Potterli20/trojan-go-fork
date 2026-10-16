package simplesocks

import (
	"net"
	"sync"
	"testing"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// wireRecorder 记下每一段落到"线上"的字节与次数。帧头与首个负载必须是同一次 Write：
// 拆成两次，mux 流上就多出一个只含帧头的段，对端状态机与分片语义都跟着变。
type wireRecorder struct {
	net.Conn
	mu  sync.Mutex
	got [][]byte
}

func (w *wireRecorder) Write(p []byte) (int, error) {
	cp := append([]byte(nil), p...)
	w.mu.Lock()
	w.got = append(w.got, cp)
	w.mu.Unlock()
	return len(p), nil
}

func (w *wireRecorder) Metadata() *tunnel.Metadata { return nil }

func (w *wireRecorder) writes() [][]byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([][]byte, len(w.got))
	copy(out, w.got)
	return out
}

func newWireConn(meta *tunnel.Metadata) (*Conn, *wireRecorder) {
	rec := &wireRecorder{}
	return &Conn{Conn: rec, metadata: meta, isOutbound: true}, rec
}

// TestConnWriteHeaderWireFormat 钉住出站帧头的线上字节。
// 这一段从 `bytes.NewBuffer(make([]byte, 0, 4096))` + 丢弃错误的 `WriteTo` 换成按
// payload 长度预留的 `AppendTo`，帧内容必须一个字节都不变：
// CMD + ATYP + 地址 + 端口 + payload（simplesocks 没有 CRLF，也没有 hash）。
// 期望值手写字面量，不复用编码器——`TestDialPacketAssociateFrameIsUnchanged` 同理。
// 这条路径是**每条 mux 流**走一次，写坏了就是所有复用流一起坏。
func TestConnWriteHeaderWireFormat(t *testing.T) {
	cases := []struct {
		name  string
		meta  *tunnel.Metadata
		frame []byte
	}{
		{
			name: "IPv4",
			meta: &tunnel.Metadata{
				Command: Connect,
				Address: &tunnel.Address{AddressType: tunnel.IPv4, IP: []byte{127, 0, 0, 1}, Port: 443},
			},
			frame: []byte{0x01, 0x01, 127, 0, 0, 1, 0x01, 0xbb},
		},
		{
			name: "域名",
			meta: &tunnel.Metadata{
				Command: Associate,
				Address: &tunnel.Address{AddressType: tunnel.DomainName, DomainName: "example.org", Port: 80},
			},
			frame: append([]byte{0x03, 0x03, 11}, append([]byte("example.org"), 0x00, 0x50)...),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newWireConn(tc.meta)
			n, err := c.Write([]byte("ping"))
			if err != nil {
				t.Fatalf("Write: %v", err)
			}
			if n != 4 {
				t.Fatalf("返回写入长度 %d，期望 4", n)
			}
			writes := rec.writes()
			if len(writes) != 1 {
				t.Fatalf("帧头与首个负载被写成了 %d 次，必须是 1 次", len(writes))
			}
			want := append(append([]byte(nil), tc.frame...), []byte("ping")...)
			if string(writes[0]) != string(want) {
				t.Fatalf("线上字节不对：\n got %q\nwant %q", writes[0], want)
			}

			// 帧头只写一次：第二次 Write 必须裸交 payload
			if _, err := c.Write([]byte("pong")); err != nil {
				t.Fatalf("第二次 Write: %v", err)
			}
			second := rec.writes()
			if len(second) != 2 || string(second[1]) != "pong" {
				t.Fatalf("第二次写成了 %q：帧头被重复发进流里", second[1:])
			}
		})
	}
}

// TestConnWriteSerializeFailureWritesNothing 钉住这次改造真正修掉的那个静默故障。
//
// 旧写法 `c.metadata.WriteTo(buf)` 的返回值被 //gosec:disable 丢掉：地址编不进线上格式
// 时缓冲区里只剩残缺字节，payload 照样跟在后面发出去，对端按状态机解就永久错位，而且
// 没有任何报错。现在必须在落笔之前返回错误，一个字都不发。
//
// headerWritten 保持 false 是有意的：出错时什么都没上线，这条流没有被污染，
// 调用方重试（或上层拆流）都比强行判死干净——这里把它钉成事实，免得将来被"顺手"改掉。
func TestConnWriteSerializeFailureWritesNothing(t *testing.T) {
	cases := []struct {
		name    string
		address *tunnel.Address
	}{
		{"nil Address", nil},
		{"非法 ATYP", &tunnel.Address{AddressType: tunnel.AddressType(99), DomainName: "example.org", Port: 443}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newWireConn(&tunnel.Metadata{Command: Connect, Address: tc.address})

			n, err := c.Write([]byte("payload"))
			if err == nil {
				t.Fatal("序列化失败必须返回错误")
			}
			if n != 0 {
				t.Fatalf("序列化失败却报告写了 %d 字节", n)
			}
			if got := rec.writes(); len(got) != 0 {
				t.Fatalf("序列化失败仍写出字节 %q：残缺帧头一旦上线，对端的状态机就永久错位", got[0])
			}
			if c.headerWritten {
				t.Fatal("序列化失败却把 headerWritten 置真：重试机会被吞掉，而流里根本没有帧头")
			}
		})
	}
}
