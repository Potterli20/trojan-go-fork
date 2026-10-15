package socks_test

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/Potterli20/trojan-go-fork/tunnel/adapter"
	"github.com/Potterli20/trojan-go-fork/tunnel/socks"
)

// socksAssociate 走一遍最小 SOCKS5 握手 + UDP ASSOCIATE，把已握手的客户端连接交出去。
func socksAssociate(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	common.Must(err)
	t.Cleanup(func() { conn.Close() })

	// greeting: VER=5, NMETHODS=1, METHOD=0(no auth)
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("send greeting: %v", err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read greeting reply: %v", err)
	}
	if reply[0] != 0x05 || reply[1] != 0x00 {
		t.Fatalf("unexpected greeting reply %x", reply)
	}

	// request: VER=5, CMD=3(ASSOCIATE), RSV=0, ATYP=1(IPv4) 0.0.0.0:0
	if _, err := conn.Write([]byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatalf("send associate request: %v", err)
	}
	// 响应是 VER/REP/RSV/ATYP + BND.ADDR(4) + BND.PORT(2)
	if _, err := io.ReadFull(conn, make([]byte, 10)); err != nil {
		t.Fatalf("read associate reply: %v", err)
	}
	return conn
}

// TestAssociateControlConnIsReclaimed 盯 ASSOCIATE 成功后那条控制连接的去向。
//
// 旧实现回完响应就让这个 goroutine 返回，连接既不下发给下游也不关闭：
// 每来一个 ASSOCIATE 就永久留下一个 fd 和一个 *Conn，对端发 FIN 也没用
// （没人再读它，本端 socket 不会因此消失）。RFC 1928 里这条连接是 UDP 关联的
// 生命周期凭据，所以正确做法不是「立刻关掉」（会打断老实客户端），
// 而是读到对端关闭为止再回收。
//
// 判据：客户端 CloseWrite（发 FIN，模拟关联结束）之后，服务端必须关掉自己那一端，
// 也就是客户端接着 Read 要看到 EOF。修复前这条 Read 一直阻塞到超时。
func TestAssociateControlConnIsReclaimed(t *testing.T) {
	port := common.PickPort("tcp", "127.0.0.1")
	ctx := t.Context()
	ctx = config.WithConfig(ctx, adapter.Name, &adapter.Config{
		LocalHost: "127.0.0.1",
		LocalPort: port,
	})
	ctx = config.WithConfig(ctx, socks.Name, &socks.Config{
		LocalHost:  "127.0.0.1",
		LocalPort:  port,
		UDPTimeout: 30,
	})
	tcpServer, err := adapter.NewServer(ctx, nil)
	common.Must(err)
	server, err := socks.NewServer(ctx, tcpServer)
	common.Must(err)

	addr := tunnel.NewAddressFromHostPort("tcp", "127.0.0.1", port).String()
	conn := socksAssociate(t, addr)
	t.Cleanup(func() { _ = server.Close() })

	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		t.Fatalf("expected *net.TCPConn, got %T", conn)
	}
	// 关联结束：客户端只关写方向，服务端读到 EOF 后就该把这条连接整个回收
	common.Must(tcp.CloseWrite())

	common.Must(conn.SetReadDeadline(time.Now().Add(5 * time.Second)))
	_, err = conn.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("服务端在关联结束后仍未关闭控制连接（fd 泄漏）")
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatalf("等待服务端关闭超时，控制连接仍被弃用：%v", err)
	}
	// 其它错误（EOF / use of closed / RST）都说明对端已经把这条连接收回了
	t.Logf("read returned %v（视为服务端已回收）", err)
}

// TestAssociateControlConnClosedByServerClose 保证在途关联不会拖住 Server.Close：
// 读循环阻塞在 conn.Read 上且不看 ctx，没有登记表 + 显式关闭的话
// Close 里的 wg.Wait 永久挂死（socks 入站被放在公网监听时，
// 这正是「建好关联就发呆」的对端能造成的常驻占用）。
func TestAssociateControlConnClosedByServerClose(t *testing.T) {
	port := common.PickPort("tcp", "127.0.0.1")
	ctx := t.Context()
	ctx = config.WithConfig(ctx, adapter.Name, &adapter.Config{
		LocalHost: "127.0.0.1",
		LocalPort: port,
	})
	ctx = config.WithConfig(ctx, socks.Name, &socks.Config{
		LocalHost:  "127.0.0.1",
		LocalPort:  port,
		UDPTimeout: 30,
	})
	tcpServer, err := adapter.NewServer(ctx, nil)
	common.Must(err)
	server, err := socks.NewServer(ctx, tcpServer)
	common.Must(err)

	addr := tunnel.NewAddressFromHostPort("tcp", "127.0.0.1", port).String()
	// 关联建立后客户端一个字都不发，也不关闭
	conn := socksAssociate(t, addr)

	closed := make(chan error, 1)
	go func() { closed <- server.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Logf("Close 返回错误（非判据）: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Server.Close 被在途 ASSOCIATE 的读循环挂死")
	}

	// 服务端退出时必须关掉这条连接：客户端 Read 应在有限时间内结束
	common.Must(conn.SetReadDeadline(time.Now().Add(5 * time.Second)))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("Close 之后服务端仍持有该控制连接")
	}
}
