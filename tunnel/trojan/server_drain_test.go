package trojan

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/statistic/memory"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type queuedConn struct {
	net.Conn
}

func (c *queuedConn) Metadata() *tunnel.Metadata { return nil }

// Close 必须排空还没被上层取走的连接队列，否则这些连接带着 fd
// 和已认证的用户身份滞留到进程结束
func TestServerCloseDrainsQueuedConns(t *testing.T) {
	Auth = nil
	ctx := config.WithConfig(t.Context(), memory.Name, &memory.Config{Passwords: []string{"password"}})
	s := newAuthTestServer(t, ctx)

	connQueued, connPeer := net.Pipe()
	muxQueued, muxPeer := net.Pipe()
	s.connChan <- &queuedConn{Conn: connQueued}
	s.muxChan <- &queuedConn{Conn: muxQueued}

	common.Must(s.Close())

	if n := len(s.connChan); n != 0 {
		t.Fatalf("connChan still holds %d connections after Close", n)
	}
	if n := len(s.muxChan); n != 0 {
		t.Fatalf("muxChan still holds %d connections after Close", n)
	}
	for name, peer := range map[string]net.Conn{"connChan": connPeer, "muxChan": muxPeer} {
		_ = peer.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := peer.Read(make([]byte, 1)); err != io.EOF {
			t.Fatalf("%s: queued connection was not closed on shutdown, got %v", name, err)
		}
		peer.Close() //gosec:disable -- 测试清理，忽略 close 错误
	}
}
