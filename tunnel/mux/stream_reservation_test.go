package mux

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type metaConn struct{ net.Conn }

func (c *metaConn) Metadata() *tunnel.Metadata { return nil }

// pipeUnderlay 每次 DialConn 交出一条 net.Pipe 连接，对端只把字节读走：
// smux 的 SYN 与关停 padding 都是裸写，没人读就会阻塞到写时限（实测 20s）。
type pipeUnderlay struct {
	mu    sync.Mutex
	peers []net.Conn
}

func (u *pipeUnderlay) DialConn(*tunnel.Address, tunnel.Tunnel) (tunnel.Conn, error) {
	a, b := net.Pipe()
	u.mu.Lock()
	u.peers = append(u.peers, b)
	u.mu.Unlock()
	go io.Copy(io.Discard, b) //gosec:disable -- 错误忽略：测试夹具
	return &metaConn{Conn: a}, nil
}

func (*pipeUnderlay) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, io.EOF
}

func (u *pipeUnderlay) Close() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, p := range u.peers {
		p.Close() //gosec:disable -- 错误忽略：测试清理
	}
	return nil
}

// 选路与 OpenStream 之间的 check-then-act：concurrency=1 时，A 已选中会话
// 但还没开流（NumStreams 仍是 0），B 在同一窗口看到 0 也选中同一会话，
// 两条流就开在同一会话上，突破 concurrency。修复后选路在锁内预约流位。
func TestStreamReservationBoundsConcurrency(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		concurrency: 1,
		timeout:     idleDefaultTimeout,
		ctx:         ctx,
		cancel:      cancel,
		clientPool:  make(map[muxID]*smuxClientInfo),
		underlay:    &pipeUnderlay{},
	}
	t.Cleanup(func() { _ = c.Close() })

	info, err := c.newMuxClient()
	if err != nil {
		t.Fatalf("newMuxClient: %v", err)
	}

	reached := make(chan struct{})
	release := make(chan struct{})
	var picks atomic.Int32
	orig := afterMuxPick
	t.Cleanup(func() { afterMuxPick = orig })
	afterMuxPick = func() {
		// 只挂起第一个调用者；第二个必须能继续走完（旧实现在这里会选中同一会话）。
		// 不能用 sync.Once.Do 包阻塞：第二个调用者会等第一个的 Do 返回，夹具自身死锁。
		if picks.Add(1) == 1 {
			close(reached)
			<-release
		}
	}

	aDone := make(chan error, 1)
	go func() {
		_, err := c.DialConn(nil, nil)
		aDone <- err
	}()
	select {
	case <-reached:
	case <-time.After(2 * time.Second):
		t.Fatal("协程 A 没有走到选路窗口，判据失效")
	}

	// B 在 A 的"已选中、未开流"窗口里再拨一条：必须改走新会话。
	if _, err := c.DialConn(nil, nil); err != nil {
		t.Fatalf("协程 B 拨号失败: %v", err)
	}
	close(release)
	select {
	case err := <-aDone:
		if err != nil {
			t.Fatalf("协程 A 拨号失败: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("协程 A 未返回")
	}

	if n := info.client.NumStreams(); n > 1 {
		t.Fatalf("原会话上开了 %d 条流，concurrency=1 被突破：选路与 OpenStream 之间没有预约", n)
	}
}