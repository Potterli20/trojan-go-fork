package proxy

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// relayConnLoop 的入站元数据守卫判据。packet 路径两处都拦了
// metadata.Address == nil（注释写得很清楚："那是空指针崩掉整个进程，不是拆一条
// 会话"），但 conn 路径只判 metadata == nil。QUIC 入站的 StreamConn.Metadata()
// 返回空壳 &tunnel.Metadata{}（Address 为 nil），经 custom 栈 [QUIC → 自拨号出站]
// 到达这里后，nil 地址被交给出站 DialConn，sink 里一句 addr.String() 就在中继
// 协程里 panic，而 proxy 包没有任何 recover —— 整个进程崩掉，不是一条会话失败。
//
// 判据不依赖真实 QUIC：用 fake source 交回空壳 Metadata，fake sink 记录是否收到
// DialConn 调用；并发执行 relayConnLoop，断言旧实现会把 nil 地址交下去、修复后
// 一个都不交且循环继续接纳后续连接（不是把整个入站停摆）。

type addresslessConn struct {
	net.Conn
	closed atomic.Bool
}

func (c *addresslessConn) Metadata() *tunnel.Metadata { return &tunnel.Metadata{} }
func (c *addresslessConn) Close() error {
	c.closed.Store(true)
	return nil
}

// addresslessServer 首次 Accept 交回空壳 Metadata，之后一直返回 io.EOF 退避，
// 由 p.ctx 结束。closed 用于确认入站连接被 relay 收尾关掉。
type addresslessServer struct {
	once   atomic.Bool
	conn   *addresslessConn
	closed chan struct{}
}

func (s *addresslessServer) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	if s.once.CompareAndSwap(false, true) {
		return s.conn, nil
	}
	return nil, io.EOF
}
func (s *addresslessServer) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, io.EOF
}
func (s *addresslessServer) Close() error {
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	return nil
}

// recordingSink 把 DialConn 收到的地址投递给通道；地址为空时也照记——判据要的
// 正是"旧实现把 nil 交下来"这个事实，而不是等它 panic。

type recordingSink struct {
	calls chan *tunnel.Address
}

func (s *recordingSink) DialConn(addr *tunnel.Address, _ tunnel.Tunnel) (tunnel.Conn, error) {
	select {
	case s.calls <- addr:
	default:
	}
	return nil, io.EOF
}
func (s *recordingSink) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, io.EOF
}
func (s *recordingSink) Close() error { return nil }

func TestRelayConnRejectsAddresslessMetadata(t *testing.T) {
	conn := &addresslessConn{}
	source := &addresslessServer{conn: conn, closed: make(chan struct{})}
	sink := &recordingSink{calls: make(chan *tunnel.Address, 4)}

	ctx, cancel := context.WithCancel(context.Background())
	p := NewProxy(ctx, cancel, []tunnel.Server{source}, sink)
	done := make(chan struct{})
	go func() { p.Run(); close(done) }()

	select {
	case addr := <-sink.calls:
		t.Fatalf("空壳 Metadata（Address=nil）被交给出站 DialConn：%v。sink 里一次 addr.String() 就崩整个进程", addr)
	case <-time.After(150 * time.Millisecond):
		// 期望：守卫拦下，sink 零调用
	}
	if !conn.closed.Load() {
		t.Fatal("空地址入站连接没有被 relay 收尾关闭")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("取消后 Relay 没有退出")
	}
}
