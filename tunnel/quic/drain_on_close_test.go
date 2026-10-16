package quic

import (
	"net"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// drainedStream 是一条"泵已经交出来、上层还没来得及取走"的 stream 会话。
type drainedStream struct {
	closed chan struct{}
}

func (d *drainedStream) Metadata() *tunnel.Metadata { return &tunnel.Metadata{} }
func (d *drainedStream) Read([]byte) (int, error)   { return 0, errDrained }
func (d *drainedStream) Write(p []byte) (int, error) {
	return len(p), nil
}
func (d *drainedStream) Close() error {
	close(d.closed)
	return nil
}
func (d *drainedStream) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (d *drainedStream) RemoteAddr() net.Addr             { return &net.UDPAddr{} }
func (d *drainedStream) SetDeadline(time.Time) error      { return nil }
func (d *drainedStream) SetReadDeadline(time.Time) error  { return nil }
func (d *drainedStream) SetWriteDeadline(time.Time) error { return nil }

// drainedSession 是服务端形态的 UDP 会话：packetBuffer 里可能还压着数据报。
type drainedSession struct {
	closed       chan struct{}
	packetBuffer chan []byte
}

func (d *drainedSession) ReadFrom([]byte) (int, net.Addr, error) { return 0, nil, errDrained }
func (d *drainedSession) WriteTo([]byte, net.Addr) (int, error)  { return 0, errDrained }
func (d *drainedSession) Close() error {
	close(d.closed)
	return nil
}
func (d *drainedSession) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (d *drainedSession) RemoteAddr() net.Addr             { return &net.UDPAddr{} }
func (d *drainedSession) SetDeadline(time.Time) error      { return nil }
func (d *drainedSession) SetReadDeadline(time.Time) error  { return nil }
func (d *drainedSession) SetWriteDeadline(time.Time) error { return nil }
func (d *drainedSession) WriteWithMetadata(p []byte, _ *tunnel.Metadata) (int, error) {
	return len(p), nil
}
func (d *drainedSession) ReadWithMetadata([]byte) (int, *tunnel.Metadata, error) {
	return 0, nil, errDrained
}

var errDrained = common.NewError("quic: drained test fixture")

// TestCloseDrainsPendingQueues 钉住本仓"入站 Close 四件事"里的最后一件：排空投递
// 缓冲队列。wg.Wait 之后生产者不再投递，但队列里可能已经躺着已经 AcceptStream
// 出来的会话——每条都攥着一个 stream/连接对象和一份深度 16 的数据报缓冲，上层没
// 来得及 AcceptConn 就关停的话只能等进程退出兜底。库化、热重载、测试反复建栈时
// 是真堆积。判据形态同 tunnel/adapter/drain_on_close_test.go。
func TestCloseDrainsPendingQueues(t *testing.T) {
	certPath, keyPath := writeSelfSigned(t, t.TempDir())
	server, err := NewServer(quicServerCtx(t, common.PickPort("udp", "127.0.0.1"), certPath, keyPath), nil)
	if err != nil {
		t.Fatalf("NewServer 失败: %v", err)
	}

	streams := make([]*drainedStream, 0, 3)
	for range 3 {
		s := &drainedStream{closed: make(chan struct{})}
		streams = append(streams, s)
		server.connChan <- s
	}
	sessions := make([]*drainedSession, 0, 2)
	for range 2 {
		pc := &drainedSession{
			closed:       make(chan struct{}),
			packetBuffer: make(chan []byte, 16),
		}
		// 模拟"泵已经收进缓冲、relay 还没来读"的驻留数据报
		pc.packetBuffer <- []byte("queued datagram")
		sessions = append(sessions, pc)
		server.packetChan <- pc
	}

	if err := server.Close(); err != nil {
		t.Logf("Close 返回（不影响判据）: %v", err)
	}

	for i, s := range streams {
		select {
		case <-s.closed:
		case <-time.After(time.Second):
			t.Fatalf("connChan 里第 %d 条会话没有被 Close 关掉：它攥着的 stream 与父连接要等进程退出才松手", i)
		}
	}
	for i, pc := range sessions {
		select {
		case <-pc.closed:
		case <-time.After(time.Second):
			t.Fatalf("packetChan 里第 %d 条 UDP 会话没有被 Close 关掉：它攥着 %d 个驻留数据报", i, len(pc.packetBuffer))
		}
	}
	if n := len(server.connChan); n != 0 {
		t.Fatalf("Close 之后 connChan 仍剩 %d 条", n)
	}
	if n := len(server.packetChan); n != 0 {
		t.Fatalf("Close 之后 packetChan 仍剩 %d 条", n)
	}
}
