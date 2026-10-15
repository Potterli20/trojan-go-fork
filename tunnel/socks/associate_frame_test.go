package socks

import (
	"bytes"
	"net"
	"testing"
)

// writeRecorder 只关心"写到线上的字节"，其余方法走一条真实的管道端。
type writeRecorder struct {
	net.Conn
	buf bytes.Buffer
}

func (c *writeRecorder) Write(p []byte) (int, error) { return c.buf.Write(p) }

// TestAssociateReplyGoldenBytes 钉住 ASSOCIATE 响应的线上字节。
//
// 这个响应是标准 socks5 客户端拿去发 UDP 的 BND.ADDR/BND.PORT，改地址组装方式
// （旧：bytes.NewBuffer(外部切片)+Address.WriteTo；新：AppendTo 就地攒）必须一个字节都不变，
// 所以判据用手写字面量而不是"再算一遍"——重算一遍只会把新实现再实现一次。
func TestAssociateReplyGoldenBytes(t *testing.T) {
	cases := []struct {
		name  string
		relay *net.UDPAddr
		want  []byte
	}{
		{
			name:  "IPv4 回环 + 10000",
			relay: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1).To4(), Port: 10000},
			want:  []byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0x27, 0x10},
		},
		{
			name:  "IPv6 回环 + 53",
			relay: &net.UDPAddr{IP: net.ParseIP("::1"), Port: 53},
			want: []byte{0x05, 0x00, 0x00, 0x04,
				0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1,
				0x00, 0x35},
		},
		{
			name:  "未指定地址 + 0 端口",
			relay: &net.UDPAddr{IP: net.IPv4zero, Port: 0},
			want:  []byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0x00, 0x00},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			listen := &fakeListenConn{feed: make(chan []byte, 1), src: tc.relay, closed: make(chan struct{})}
			s := &Server{listenPacketConn: listen}
			rec := &writeRecorder{}

			if err := s.associate(rec, nil); err != nil {
				t.Fatalf("associate: %v", err)
			}
			got := rec.buf.Bytes()
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("ASSOCIATE 响应字节变了\n got % x\nwant % x", got, tc.want)
			}
		})
	}
}
