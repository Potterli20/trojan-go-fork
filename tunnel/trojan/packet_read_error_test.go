package trojan

import (
	"bytes"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// stubConn 只实现 Read/Close/Addr，用来把一段可控字节喂给 ReadWithMetadata。
type stubConn struct {
	*bytes.Reader
	closed bool
}

func (c *stubConn) Close() error                { c.closed = true; return nil }
func (c *stubConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *stubConn) LocalAddr() net.Addr         { return &net.TCPAddr{} }
func (c *stubConn) RemoteAddr() net.Addr        { return &net.TCPAddr{} }
func (c *stubConn) SetDeadline(time.Time) error { return nil }
func (c *stubConn) SetReadDeadline(time.Time) error {
	return nil
}
func (c *stubConn) SetWriteDeadline(time.Time) error { return nil }
func (c *stubConn) Metadata() *tunnel.Metadata       { return nil }

func ipv4AddrFrame() []byte {
	addr := &tunnel.Address{AddressType: tunnel.IPv4, IP: []byte{1, 2, 3, 4}, Port: 53}
	buf, err := addr.AppendTo(nil)
	if err != nil {
		panic(err)
	}
	return buf
}

// 长度/CRLF/payload 三段 ReadFull 的短读必须把底层错误（ErrUnexpectedEOF）挂进错误链。
// 旧实现丢弃 err，日志里只剩一句无根因的 "failed to read length"，无法区分 EOF/reset/超时。
func TestPacketReadShortErrorsCarryCause(t *testing.T) {
	cases := []struct {
		name string
		wire []byte
		want string
	}{
		{
			name: "length",
			wire: func() []byte { return ipv4AddrFrame() }(), // 地址后没有任何字节
			want: "failed to read length",
		},
		{
			name: "crlf",
			wire: func() []byte {
				b := ipv4AddrFrame()
				return append(b, 0, 5) // 有长度，缺 CRLF
			}(),
			want: "failed to read crlf",
		},
		{
			name: "payload",
			wire: func() []byte {
				b := ipv4AddrFrame()
				return append(b, 0, 5, '\r', '\n', 'a', 'b') // 声明 5 字节，只给 2
			}(),
			want: "failed to read payload",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pc := &PacketConn{Conn: &stubConn{Reader: bytes.NewReader(tc.wire)}}
			_, _, err := pc.ReadWithMetadata(make([]byte, 64))
			if err == nil {
				t.Fatal("短读应当返回错误")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("错误 %q 缺少期望的上下文 %q", err, tc.want)
			}
			if !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
				t.Fatalf("错误链丢失了底层短读原因：%q", err)
			}
		})
	}
}
