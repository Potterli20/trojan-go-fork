package trojan

import (
	"context"
	"net"
	"testing"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type twiceClosedConn struct {
	tunnel.Conn
}

func (c *twiceClosedConn) Close() error { return net.ErrClosed }

// TestOutboundConnCloseToleratesAlreadyClosed 盯的是关停噪音被当成故障：proxy 的转发环
// 与本 tunnel 都会去关同一条流，谁后关谁拿到 net.ErrClosed。原实现把它记成 ERROR
// 并原样上抛，于是每次正常关停的日志里都有一行"Failed to close connection:
// use of closed network connection"（真实进程里实测到），退出码判断也被污染。
func TestOutboundConnCloseToleratesAlreadyClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &OutboundConn{
		Conn:     &twiceClosedConn{},
		metadata: &tunnel.Metadata{Address: &tunnel.Address{AddressType: tunnel.IPv4, Port: 443}},
		ctx:      ctx,
		cancel:   cancel,
	}

	if err := c.Close(); err != nil {
		t.Fatalf("对已关闭的流再关一次被当成错误上抛: %v", err)
	}
}
