package simplesocks

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// discardConn 是一个永远写成功的 tunnel.Conn，用来量首帧拼接这一段的分配。
type discardConn struct{}

func (discardConn) Metadata() *tunnel.Metadata       { return nil }
func (discardConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (discardConn) Write(p []byte) (int, error)      { return len(p), nil }
func (discardConn) Close() error                     { return nil }
func (discardConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (discardConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (discardConn) SetDeadline(time.Time) error      { return nil }
func (discardConn) SetReadDeadline(time.Time) error  { return nil }
func (discardConn) SetWriteDeadline(time.Time) error { return nil }

var benchMeta = &tunnel.Metadata{
	Command: Connect,
	Address: &tunnel.Address{AddressType: tunnel.DomainName, DomainName: "www.example.com", Port: 443},
}

// BenchmarkConnWriteFirstFrame 量"帧头 + 首个负载拼成一次 Write"这段的分配。
// 这条路径是**每条 mux 流**一次（不是每包），所以判读标准与每包路径不同：
// 300 字节负载下实测 1 alloc/op、384 B/op——是否值得再套一层池，看
// pool_account 那套借用/归还的复杂度换来的收益，结论记在 AGENTS.md。
func BenchmarkConnWriteFirstFrame(b *testing.B) {
	payload := make([]byte, 300)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c := &Conn{Conn: discardConn{}, metadata: benchMeta, isOutbound: true}
		if _, err := c.Write(payload); err != nil {
			b.Fatal(err)
		}
	}
}
