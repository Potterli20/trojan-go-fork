package trojan

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/redirector"
	"github.com/Potterli20/trojan-go-fork/statistic/memory"
)

// TestServerCloseReclaimsInflightRedirection 盯住"入站关掉之后不再有活的重定向会话"。
//
// Redirector 的 ctx 派生自 Server 的 ctx，所以 s.cancel() 能叫醒**已经在中继**的会话
// （superviseCopies 见到 ctx.Done 会关两端）；但排在队列里（容量 64）还没被 worker
// 取走的请求，只有 Redirector.Close 的排空循环会去 closeDropped —— worker 走 ctx.Done
// 分支是直接 return，不排空。过去全仓四个创建点都没有调用 Close，那条排空逻辑等于死代码。
//
// 这里能确定复现的是"会话在关停后仍然活着"这一类回归：把接线去掉、或者把 Redirector
// 改成挂在 context.Background() 上（丢掉继承关系），这条判据都会红。至于队列里那一批，
// 由 redirector 包内的 TestCloseDrainsQueuedRedirections 直接钉排空本身。
func TestServerCloseReclaimsInflightRedirection(t *testing.T) {
	Auth = nil
	ctx := config.WithConfig(t.Context(), memory.Name, &memory.Config{Passwords: []string{"password"}})
	s := newAuthTestServer(t, ctx)

	// 诱饵服务器：accept 之后一个字都不发也不关，于是中继停在"读入站"这一侧
	decoy, err := net.Listen("tcp", "127.0.0.1:0")
	common.Must(err)
	defer decoy.Close() //gosec:disable -- 错误忽略：测试清理
	held := make(chan net.Conn, 8)
	go func() {
		for {
			c, err := decoy.Accept()
			if err != nil {
				return
			}
			held <- c
		}
	}()
	defer func() {
		for {
			select {
			case c := <-held:
				c.Close() //gosec:disable -- 错误忽略：测试清理
			default:
				return
			}
		}
	}()

	inbound, peer := net.Pipe()
	s.redir.Redirect(&redirector.Redirection{
		Dial: func(addr net.Addr) (net.Conn, error) {
			return net.Dial("tcp", addr.String())
		},
		RedirectTo:  decoy.Addr(),
		InboundConn: inbound,
	})

	// 确认 worker 已经接手并把会话拨到诱饵上，此后它阻塞在读入站
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("重定向请求没有被 worker 接手")
	}

	common.Must(s.Close())

	// 关停之后入站必须被关闭：对端读要立刻拿到 EOF。
	// 这里不能用 SetDeadline——net.Pipe 在**远端**已关时就直接返回 ErrClosedPipe
	// （src/net/pipe.go:210-217），拿它当"限时读"会让判据在修复成功的路径上先炸掉。
	read := make(chan error, 1)
	go func() {
		_, err := peer.Read(make([]byte, 1))
		read <- err
	}()
	select {
	case err := <-read:
		if err != io.EOF {
			t.Fatalf("Server.Close 后入站连接的读返回 %v，期望 io.EOF（连接已被回收）", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Server.Close 没有回收在途重定向会话，入站连接仍被攥着；" +
			"没接上 Redirector.Close 时它要等满 redirectionIdleTimeout 才松开")
	}
}
