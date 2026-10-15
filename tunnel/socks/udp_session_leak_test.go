package socks

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// errFakeWrite 注入给 listenPacketConn，模拟出网 socket 写失败。
var errFakeWrite = errors.New("socks test: forced WriteTo failure")

// fakeListenConn 只提供 packetDispatchLoop 和会话守护协程真正会用到的方法。
// ReadFrom 从 feed 里取"从外网收到的包"，WriteTo 可按需判失败。
type fakeListenConn struct {
	feed      chan []byte
	src       net.Addr
	writeErr  error
	closed    chan struct{}
	closeOnce sync.Once
}

func (c *fakeListenConn) ReadFrom(p []byte) (int, net.Addr, error) {
	select {
	case b := <-c.feed:
		return copy(p, b), c.src, nil
	case <-c.closed:
		return 0, nil, net.ErrClosed
	}
}

func (c *fakeListenConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	return len(p), nil
}

func (c *fakeListenConn) LocalAddr() net.Addr              { return c.src }
func (c *fakeListenConn) RemoteAddr() net.Addr             { return c.src }
func (c *fakeListenConn) SetDeadline(time.Time) error      { return nil }
func (c *fakeListenConn) SetReadDeadline(time.Time) error  { return nil }
func (c *fakeListenConn) SetWriteDeadline(time.Time) error { return nil }

func (c *fakeListenConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

// 下面两个方法属于 tunnel.PacketConn 接口，但中继实际用的是会话自己的实现，
// 这里不会被调用。
func (c *fakeListenConn) WriteWithMetadata([]byte, *tunnel.Metadata) (int, error) {
	return 0, errors.New("not used by socks dispatch")
}

func (c *fakeListenConn) ReadWithMetadata([]byte) (int, *tunnel.Metadata, error) {
	return 0, nil, errors.New("not used by socks dispatch")
}

// socksPacket 按 SOCKS5 UDP 请求格式造一个"外网来的包"：RSV(2)+FRAG(1)+地址+负载。
// 地址取 127.0.0.1:53，保证能被 tunnel.Address.ReadFrom 正常解析。
func socksPacket(payload string) []byte {
	p := []byte{0, 0, 0, byte(tunnel.IPv4), 127, 0, 0, 1, 0, 53}
	return append(p, []byte(payload)...)
}

// newDispatchServer 起一个只喂 fakeListenConn 的 socks Server，timeout 故意设成 5 分钟：
// 这样"表项最终会被 timer 分支清掉"这条自愈路径远在水准线之外，测的就是不能自愈的窗口。
func newDispatchServer(t *testing.T, writeErr error) (*Server, *fakeListenConn, net.Addr) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	src := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 41234}
	listen := &fakeListenConn{
		feed:     make(chan []byte, 8),
		src:      src,
		writeErr: writeErr,
		closed:   make(chan struct{}),
	}
	s := &Server{
		packetChan:       make(chan tunnel.PacketConn, 8),
		timeout:          5 * time.Minute,
		listenPacketConn: listen,
		mapping:          map[string]*PacketConn{},
		ctx:              ctx,
		cancel:           cancel,
	}
	go s.packetDispatchLoop()
	return s, listen, src
}

// acceptSession 取走 dispatch 投递出来的会话。
func acceptSession(t *testing.T, s *Server) *PacketConn {
	t.Helper()
	select {
	case pc := <-s.packetChan:
		conn, ok := pc.(*PacketConn)
		if !ok {
			t.Fatalf("投递的不是 *socks.PacketConn: %T", pc)
		}
		return conn
	case <-time.After(2 * time.Second):
		t.Fatal("首个包没有投递出 UDP 会话")
	}
	return nil
}

// waitMappingGone 等表项被回收；超时即判定泄露。
func waitMappingGone(s *Server, key string) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mappingLock.RLock()
		_, alive := s.mapping[key]
		s.mappingLock.RUnlock()
		if !alive {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// feedAndAccept 重发同源包直到投递出新会话，返回该会话（拿不到则 nil）。
func feedAndAccept(t *testing.T, s *Server, listen *fakeListenConn) tunnel.PacketConn {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case listen.feed <- socksPacket("again"):
		case <-time.After(20 * time.Millisecond):
			continue
		}
		select {
		case fresh := <-s.packetChan:
			if fresh != nil {
				return fresh
			}
		case <-time.After(100 * time.Millisecond):
		}
	}
	return nil
}

// TestUDPSessionMappingReclaimedOnWriteFailure 钉住守护协程的写失败出口。
//
// listenPacketConn.WriteTo 失败时 daemon 直接 return，而旧代码把 delete(s.mapping, …)
// 只写在 timer.C 与 ctx.Done() 两个 select 分支里，这条 return 绕过了它：
// 表项 + input/output 两条 128 深度通道永久滞留，且该源地址之后的每个包都因为
// found 恒真被投进这个已无人读的死会话（select/default 丢弃），同一个源端口
// 重开会话就是彻底黑洞。socks 与 dokodemo/tproxy 是同型 daemon，那两处已改成
// defer 统一回收，这里是漏改。
func TestUDPSessionMappingReclaimedOnWriteFailure(t *testing.T) {
	s, listen, src := newDispatchServer(t, errFakeWrite)
	key := src.String()

	listen.feed <- socksPacket("first")
	acceptSession(t, s)

	// 回包方向：daemon 会走 WriteTo 失败分支
	pc := s.mapping[key]
	if pc == nil {
		t.Fatal("首包没有登记 mapping 表项")
	}
	if _, err := pc.WriteWithMetadata([]byte("resp"), &tunnel.Metadata{
		Address: &tunnel.Address{AddressType: tunnel.IPv4, IP: net.IPv4(10, 0, 0, 1), Port: 443},
	}); err != nil {
		t.Fatalf("WriteWithMetadata: %v", err)
	}

	if !waitMappingGone(s, key) {
		t.Fatal("WriteTo 失败后 mapping 表项永久残留：daemon 的这条 return 没走回收逻辑")
	}
	if fresh := feedAndAccept(t, s, listen); fresh == nil {
		t.Fatal("源地址在写失败后再也建不出新会话（黑洞）")
	}
}

// TestUDPSessionMappingReclaimedOnSerializeFailure 钉住同一个 daemon 的地址序列化失败出口。
// 判据与上一条相同，只是触发点换成 Address.AppendTo 返回错误（非法 ATYP）。
func TestUDPSessionMappingReclaimedOnSerializeFailure(t *testing.T) {
	s, listen, src := newDispatchServer(t, nil)
	key := src.String()

	listen.feed <- socksPacket("first")
	acceptSession(t, s)

	pc := s.mapping[key]
	if pc == nil {
		t.Fatal("首包没有登记 mapping 表项")
	}
	if _, err := pc.WriteWithMetadata([]byte("resp"), &tunnel.Metadata{
		// 非法 ATYP：AppendTo 必然报错，daemon 走序列化失败分支
		Address: &tunnel.Address{AddressType: tunnel.AddressType(99), DomainName: "example.com", Port: 443},
	}); err != nil {
		t.Fatalf("WriteWithMetadata: %v", err)
	}

	if !waitMappingGone(s, key) {
		t.Fatal("地址序列化失败后 mapping 表项永久残留：daemon 的这条 return 没走回收逻辑")
	}
	if fresh := feedAndAccept(t, s, listen); fresh == nil {
		t.Fatal("源地址在序列化失败后再也建不出新会话（黑洞）")
	}
}
