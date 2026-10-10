package dokodemo

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// blockedUDP 是一个可控的 net.PacketConn：ReadFrom 从 feed 取包，WriteTo 在 gate
// 打开前一直阻塞。用它是因为要复现的缺陷只在"写把会话 goroutine 卡到计时器过期"
// 之后才会暴露。
type blockedUDP struct {
	feed   chan []byte
	src    net.Addr
	gate   chan struct{}
	closed chan struct{}
	once   sync.Once
}

func (u *blockedUDP) ReadFrom(p []byte) (int, net.Addr, error) {
	select {
	case b := <-u.feed:
		return copy(p, b), u.src, nil
	case <-u.closed:
		return 0, nil, net.ErrClosed
	}
}

func (u *blockedUDP) WriteTo(p []byte, _ net.Addr) (int, error) {
	select {
	case <-u.gate:
		return len(p), nil
	case <-u.closed:
		return 0, net.ErrClosed
	}
}

func (u *blockedUDP) LocalAddr() net.Addr              { return u.src }
func (u *blockedUDP) RemoteAddr() net.Addr             { return u.src }
func (u *blockedUDP) SetDeadline(time.Time) error      { return nil }
func (u *blockedUDP) SetReadDeadline(time.Time) error  { return nil }
func (u *blockedUDP) SetWriteDeadline(time.Time) error { return nil }

func (u *blockedUDP) Close() error {
	u.once.Do(func() { close(u.closed) })
	return nil
}

// TestUDPSessionSurvivesTimerRestartWhileWriting 覆盖此前完全没测过的一条路径：
// 会话 goroutine 在 udpListener.WriteTo 里卡到空闲计时器过期之后，仍能正常重启计时器
// 并按超时退出、把 mapping 表项清掉。
//
// 顺带记录一次实测结论（Go 1.27.1，time/sleep.go:77-83）：这三处
//
//	if !timer.Stop() { <-timer.C }
//	timer.Reset(...)
//
// 是 Go 1.23 之前的遗留排空写法。曾经怀疑它在 1.23+ 会永久阻塞（"Stop 之后再收 t.C
// 保证阻塞而不是拿到过期值"）。用最小实验证伪了：计时器在**无人接收**的情况下过期时，
// Stop() 返回 **true**，所以那条排空分支根本不会执行；而循环里唯一收 timer.C 的分支
// 收到就 return，也不可能出现 false。结论是它只是一条永不命中的死支路，不是死锁——
// 因此本测试是路径覆盖，不是缺陷门禁。
func TestUDPSessionSurvivesTimerRestartWhileWriting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40000}
	udp := &blockedUDP{
		feed:   make(chan []byte, 4),
		src:    src,
		gate:   make(chan struct{}),
		closed: make(chan struct{}),
	}
	defer close(udp.closed)

	s := &Server{
		udpListener: udp,
		packetChan:  make(chan tunnel.PacketConn, 8),
		timeout:     60 * time.Millisecond,
		targetAddr:  &tunnel.Address{AddressType: tunnel.IPv4, Port: 53},
		mapping:     map[string]*PacketConn{},
		ctx:         ctx,
		cancel:      cancel,
	}

	go s.dispatchLoop()

	// 第一个包：建立会话并投递给下游
	udp.feed <- []byte("ping")
	pc, err := s.AcceptPacket(&Tunnel{})
	if err != nil {
		t.Fatalf("AcceptPacket: %v", err)
	}
	conn, ok := pc.(*PacketConn)
	if !ok {
		t.Fatalf("AcceptPacket 返回了意外类型 %T", pc)
	}

	written := make(chan error, 1)
	go func() {
		_, err := conn.WriteWithMetadata([]byte("pong"), &tunnel.Metadata{Command: tunnel.Command(0x03)})
		written <- err
	}()

	// 让计时器在写阻塞期间到期（timeout 60ms，等 200ms 足够）
	time.Sleep(200 * time.Millisecond)
	close(udp.gate) // 放行：会话 goroutine 接下来就要重启计时器

	select {
	case err := <-written:
		if err != nil {
			t.Fatalf("WriteWithMetadata 失败: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("响应包没有送达 output：会话 goroutine 在写之前就卡住了")
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.mappingLock.Lock()
		_, alive := s.mapping[src.String()]
		s.mappingLock.Unlock()
		if !alive {
			return // 会话按超时正常退出，mapping 已回收
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("会话 goroutine 没有退出、mapping 条目永久残留：timer.Stop() 返回 false 后的 " +
		"<-timer.C 在 Go 1.23+ 保证永久阻塞（time/sleep.go:77-83）")
}
