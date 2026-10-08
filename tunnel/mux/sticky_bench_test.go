package mux

import (
	"io"
	"net"
	"testing"
	"time"
)

// discardConn 是一个吞掉一切写入的 net.Conn，用来把测量范围限制在
// stickToPayload 自身（粘连与拷贝），不含下层协议开销
type discardConn struct{}

func (discardConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (discardConn) Write(p []byte) (int, error)      { return len(p), nil }
func (discardConn) Close() error                     { return nil }
func (discardConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (discardConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (discardConn) SetDeadline(time.Time) error      { return nil }
func (discardConn) SetReadDeadline(time.Time) error  { return nil }
func (discardConn) SetWriteDeadline(time.Time) error { return nil }

// BenchmarkStickyConnWrite 度量中继热路径上每次数据写的分配。
// mux 开启时，proxy 层每搬一个 chunk 就会走到这里一次，所以这个 B/op
// 直接换算成 GC 压力；期望是快路径下不产生任何分配。
func BenchmarkStickyConnWrite(b *testing.B) {
	sc := newStickyConn(&pipeTunnelConn{Conn: discardConn{}})
	payload := make([]byte, 8*1024)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := sc.Write(payload); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStickyConnWriteWithHeader 覆盖需要粘连帧头的慢路径：
// 此时必须拷贝，不能退回零拷贝
func BenchmarkStickyConnWriteWithHeader(b *testing.B) {
	sc := newStickyConn(&pipeTunnelConn{Conn: discardConn{}})
	payload := make([]byte, 8*1024)
	b.ReportAllocs()
	for b.Loop() {
		sc.synQueue <- []byte{1, 0, 0, 0, 0, 0, 0, 0}
		if _, err := sc.Write(payload); err != nil {
			b.Fatal(err)
		}
	}
}
