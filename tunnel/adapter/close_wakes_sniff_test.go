package adapter

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type readProbe struct {
	net.Conn
	started chan struct{}
	once    sync.Once
}

func (p *readProbe) Read(b []byte) (int, error) {
	p.once.Do(func() { close(p.started) })
	return p.Conn.Read(b)
}

type fakeTCPListener struct {
	conn   net.Conn
	closed chan struct{}
	once   sync.Once
}

func (l *fakeTCPListener) Accept() (net.Conn, error) {
	if l.conn != nil {
		c := l.conn
		l.conn = nil
		return c, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}

func (l *fakeTCPListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (*fakeTCPListener) Addr() net.Addr { return &net.TCPAddr{} }

type fakePacketListener struct {
	closed chan struct{}
	once   sync.Once
}

func (l *fakePacketListener) ReadFrom([]byte) (int, net.Addr, error) {
	<-l.closed
	return 0, nil, net.ErrClosed
}

func (l *fakePacketListener) WriteTo(p []byte, _ net.Addr) (int, error) { return len(p), nil }

func (l *fakePacketListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (*fakePacketListener) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (*fakePacketListener) SetDeadline(time.Time) error      { return nil }
func (*fakePacketListener) SetReadDeadline(time.Time) error  { return nil }
func (*fakePacketListener) SetWriteDeadline(time.Time) error { return nil }

// 静默对端让 adapter 嗅探读卡在 firstByteTimeout(10s) 里时，Close 必须当场
// 叫醒它：嗅探协程记在 Server.wg 里，读不返回 wg.Wait 就要白等满时限。
func TestCloseWakesSniff(t *testing.T) {
	peer, raw := net.Pipe()
	t.Cleanup(func() { peer.Close() }) //gosec:disable -- 错误忽略：测试清理

	probe := &readProbe{Conn: raw, started: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		tcpListener: &fakeTCPListener{conn: probe, closed: make(chan struct{})},
		udpListener: &fakePacketListener{closed: make(chan struct{})},
		socksConn:   make(chan tunnel.Conn, 4),
		httpConn:    make(chan tunnel.Conn, 4),
		ctx:         ctx,
		cancel:      cancel,
	}
	s.wg.Go(s.acceptConnLoop)

	select {
	case <-probe.started:
	case <-time.After(2 * time.Second):
		t.Fatal("嗅探读没有开始，判据失效")
	}

	start := time.Now()
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		_ = s.Close()
	}()
	select {
	case <-closed:
		t.Logf("Close 在 %v 内叫醒了嗅探读", time.Since(start).Round(time.Millisecond))
	case <-time.After(2 * time.Second):
		t.Fatal("Close 后 2s 仍未返回：关停要白等满 10s 嗅探时限")
	}
}
