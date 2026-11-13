package socks

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// readProbe 在第一次 Read 被调用时点亮信号：握手协程确实已经进入
// 「阻塞在对端字节上」这段窗口，判据不用墙钟去猜。
type readProbe struct {
	net.Conn
	started chan struct{}
	once    sync.Once
}

func (p *readProbe) Read(b []byte) (int, error) {
	p.once.Do(func() { close(p.started) })
	return p.Conn.Read(b)
}

func (p *readProbe) Metadata() *tunnel.Metadata { return nil }

type handshakeUnderlay struct {
	conn   tunnel.Conn
	closed chan struct{}
	once   sync.Once
}

func (u *handshakeUnderlay) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	if u.conn != nil {
		c := u.conn
		u.conn = nil
		return c, nil
	}
	<-u.closed
	return nil, io.EOF
}

func (*handshakeUnderlay) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, io.EOF
}

func (u *handshakeUnderlay) Close() error {
	u.once.Do(func() { close(u.closed) })
	return nil
}

// 静默对端让 socks 握手读卡在 handshakeTimeout(30s) 里时，Close 必须当场叫醒它。
func TestCloseWakesHandshake(t *testing.T) {
	peer, raw := net.Pipe()
	t.Cleanup(func() { peer.Close() }) //gosec:disable -- 错误忽略：测试清理

	probe := &readProbe{Conn: raw, started: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		underlay:         &handshakeUnderlay{conn: probe, closed: make(chan struct{})},
		connChan:         make(chan tunnel.Conn, 4),
		packetChan:       make(chan tunnel.PacketConn, 4),
		mapping:          make(map[tunnel.SessionKey]*PacketConn),
		associations:     make(map[tunnel.Conn]struct{}),
		listenPacketConn: &multiSrcListenConn{feed: make(chan srcPacket), closed: make(chan struct{})},
		ctx:              ctx,
		cancel:           cancel,
	}
	s.wg.Go(s.acceptConnLoop)

	select {
	case <-probe.started:
	case <-time.After(2 * time.Second):
		t.Fatal("握手读没有开始，判据失效")
	}

	start := time.Now()
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		_ = s.Close()
	}()
	select {
	case <-closed:
		t.Logf("Close 在 %v 内叫醒了握手读", time.Since(start).Round(time.Millisecond))
	case <-time.After(2 * time.Second):
		t.Fatal("Close 后 2s 仍未返回：关停要白等满 30s 握手时限")
	}
}
