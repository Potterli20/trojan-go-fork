package adapter

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type pipeTunnelConn struct {
	net.Conn
}

func (c *pipeTunnelConn) Metadata() *tunnel.Metadata { return &tunnel.Metadata{} }

// TestCloseDrainsQueuedConns 钉住入站队列在关停时的回收。
//
// adapter 的 Close 过去只做 cancel + 关监听 + wg.Wait，两条投递缓冲
// （socksConn / httpConn 各 32）里"已经嗅完协议、但上层还没来得及 AcceptConn 取走"
// 的连接就此没人管了。每条都攥着一条已经 Accept 出来的客户端 TCP 连接，也就是一个 fd；
// 进程退出能兜住它，但库化调用、api 反复建栈、热重载这些场景不会退出——堆积是实打实的。
// trojan/tls/transport 三层早就有这段排空，其余层是漏改。
func TestCloseDrainsQueuedConns(t *testing.T) {
	var server *Server
	for range 5 {
		port := common.PickPort("tcp", "127.0.0.1")
		ctx := config.WithConfig(context.Background(), Name, &Config{
			LocalHost: "127.0.0.1",
			LocalPort: port,
		})
		s, err := NewServer(ctx, nil)
		if err == nil {
			server = s
			break
		}
	}
	if server == nil {
		t.Fatal("多次尝试仍无法建立 adapter 监听")
	}

	peers := make([]net.Conn, 0, 3)
	for range 2 { // socksConn 灌两条
		a, b := net.Pipe()
		peers = append(peers, b)
		server.socksConn <- &pipeTunnelConn{Conn: a}
	}
	{ // httpConn 灌一条
		a, b := net.Pipe()
		peers = append(peers, b)
		server.httpConn <- &pipeTunnelConn{Conn: a}
	}

	common.Must(server.Close())

	if n := len(server.socksConn); n != 0 {
		t.Fatalf("Close 之后 socksConn 仍压着 %d 条连接", n)
	}
	if n := len(server.httpConn); n != 0 {
		t.Fatalf("Close 之后 httpConn 仍压着 %d 条连接", n)
	}
	// 判据：对端读要拿到 EOF/错误，说明服务端那一侧被关掉了。
	// 不能用 SetDeadline——net.Pipe 在**远端**已关时它直接返回 ErrClosedPipe
	// （src/net/pipe.go:210-217），那正是修复成功的样子，却被当成测试失败。
	for i, p := range peers {
		closed := make(chan error, 1)
		go func(c net.Conn) {
			_, err := c.Read(make([]byte, 1))
			closed <- err
		}(p)
		select {
		case err := <-closed:
			if err == nil {
				t.Fatalf("第 %d 条滞留连接的读竟然成功了", i)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("第 %d 条滞留在投递缓冲里的连接没有被关停（fd 泄漏）", i)
		}
		p.Close() //gosec:disable -- 错误忽略：测试清理
	}
}
