package tunnel

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

// guardFakeConn 只为让 AcceptConnGuarded 有一条可以交回的连接。
type guardFakeConn struct {
	net.Conn
}

func (guardFakeConn) Metadata() *Metadata { return nil }

// guardPanicServer 前 panicTimes 次 AcceptConn 在解析"对端可控字节"时 panic，
// 之后按 delivered 交付；accepts 记录被调用了几次，用来证明循环没有停。
type guardPanicServer struct {
	panicTimes int
	accepts    int
	delivered  Conn
	errOnCall  error
}

func (f *guardPanicServer) AcceptConn(Tunnel) (Conn, error) {
	f.accepts++
	if f.accepts <= f.panicTimes {
		panic("malformed peer bytes")
	}
	if f.errOnCall != nil {
		return nil, f.errOnCall
	}
	return f.delivered, nil
}

func (f *guardPanicServer) AcceptPacket(Tunnel) (PacketConn, error) {
	return nil, errors.New("not used")
}

func (f *guardPanicServer) Close() error { return nil }

type guardTunnel struct{}

func (guardTunnel) Name() string { return "GUARDTEST" }

func (guardTunnel) NewClient(context.Context, Client) (Client, error) {
	return nil, errors.New("not used")
}

func (guardTunnel) NewServer(context.Context, Server) (Server, error) {
	return nil, errors.New("not used")
}

func TestAcceptConnGuardedTurnsPanicIntoSentinel(t *testing.T) {
	srv := &guardPanicServer{panicTimes: 1}
	conn, err := AcceptConnGuarded(srv, guardTunnel{})
	if conn != nil {
		t.Fatal("panic 之后不该交回连接对象")
	}
	if !errors.Is(err, ErrAcceptPanic) {
		t.Fatalf("panic 没有被转成 ErrAcceptPanic，父层循环无法据此继续接纳：%v", err)
	}
	if !strings.Contains(err.Error(), "GUARDTEST.AcceptConn") {
		t.Fatalf("错误文本里没有指出是哪一层panic：%v", err)
	}
	if !strings.Contains(err.Error(), "malformed peer bytes") {
		t.Fatalf("panic 值丢了，排查时看不到对端字节把哪里打崩：%v", err)
	}

	// 边界之后循环必须还能继续接纳：同一个 server 再走一次，这次交付成功连接
	taken := guardFakeConn{}
	srv.delivered = taken
	conn, err = AcceptConnGuarded(srv, guardTunnel{})
	if err != nil {
		t.Fatalf("第二次接纳失败，边界把循环一并弄坏了：%v", err)
	}
	if conn != taken {
		t.Fatal("成功路径上的连接对象没有原样交回")
	}
	if srv.accepts != 2 {
		t.Fatalf("underlay 被调用 %d 次，期望 2 次", srv.accepts)
	}
}

// TestAcceptConnGuardedKeepsRealErrorsApart 钉住"panic 哨兵"与真实 accept 失败的分界：
// 下层返回的普通错误必须原样透传、且不被标成 panic。否则关停路径
// （ctx 已取消时各层返回的错误）会被循环当成"这一层还活着"而无限 continue 下去。
func TestAcceptConnGuardedKeepsRealErrorsApart(t *testing.T) {
	boom := errors.New("underlay listener is gone")
	srv := &guardPanicServer{errOnCall: boom}
	conn, err := AcceptConnGuarded(srv, guardTunnel{})
	if conn != nil {
		t.Fatal("出错时不该交回连接")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("普通 accept 错误被改写了：%v", err)
	}
	if errors.Is(err, ErrAcceptPanic) {
		t.Fatal("普通 accept 错误被误标成 panic 哨兵")
	}
}

func TestGuardPanicPassesThroughErrors(t *testing.T) {
	boom := errors.New("header read failed")
	err := GuardPanic("x.readHeader", func() error { return boom })
	if !errors.Is(err, boom) || errors.Is(err, ErrAcceptPanic) {
		t.Fatalf("非 panic 的错误被包装/误标了：%v", err)
	}
	var hit bool
	err = GuardPanic("x.readHeader", func() error { hit = true; panic("bad peer header") })
	if !hit {
		t.Fatal("fn 没有被执行，判据失效")
	}
	if !errors.Is(err, ErrAcceptPanic) {
		t.Fatalf("panic 没有变成 ErrAcceptPanic：%v", err)
	}
	// GuardPanic 自己不能把 panic 再抛出去
}
