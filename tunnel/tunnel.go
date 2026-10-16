package tunnel

import (
	"context"
	"io"
	"net"
	"sync"

	"github.com/Potterli20/trojan-go-fork/common"
)

// Conn is the TCP connection in the tunnel
//
// 跨层契约（由 relay 的复用节奏决定，不是某个实现的细节）：`Write(p)` 返回之后，
// 实现不得再保留、读取或以任何方式别名 p。`proxy/proxy.go` 的 relayConnLoop 用
// boundedBufPool 的 buffer 喂 `io.CopyBuffer`，Write 一返回这块就接着被读下一批字节。
// 需要跨本次调用持有的（入队、异步组帧、缓存起来晚点发）必须自己 copy。
type Conn interface {
	net.Conn
	Metadata() *Metadata
}

// PacketConn is the UDP packet stream in the tunnel
//
// 同一份契约在 UDP 侧更硬：`relayPacketLoop` 是 `WriteWithMetadata(buf[:n], m)` 返回后
// **立刻** `packetPool.Put(buf)`，下一个包 Get 到同一块底层数组的概率极高——存引用不会
// 当场出错，只会在包与包之间把别人的负载串进来。`ReadWithMetadata` 交回的 `*Metadata`
// 同理：relay 会在读完之后把它的 payload buffer 归还池，所以返回的 metadata 不得引用
// 那块 buffer（`tunnel/freedom/pool_alias_test.go` 钉的就是这一条）。
// 判据见 tunnel/socks/no_retain_test.go、tunnel/dokodemo/no_retain_test.go、
// tunnel/trojan/packet_write_test.go。
type PacketConn interface {
	net.PacketConn
	WriteWithMetadata([]byte, *Metadata) (int, error)
	ReadWithMetadata([]byte) (int, *Metadata, error)
}

// ConnDialer creates TCP connections from the tunnel
type ConnDialer interface {
	DialConn(*Address, Tunnel) (Conn, error)
}

// PacketDialer creates UDP packet stream from the tunnel
type PacketDialer interface {
	DialPacket(Tunnel) (PacketConn, error)
}

// ConnListener accept TCP connections
type ConnListener interface {
	AcceptConn(Tunnel) (Conn, error)
}

// PacketListener accept UDP packet stream
// We don't have any tunnel based on packet streams, so AcceptPacket will always receive a real PacketConn
type PacketListener interface {
	AcceptPacket(Tunnel) (PacketConn, error)
}

// Dialer can dial to original server with a tunnel
type Dialer interface {
	ConnDialer
	PacketDialer
}

// Listener can accept TCP and UDP streams from a tunnel
type Listener interface {
	ConnListener
	PacketListener
}

// Client is the tunnel client based on stream connections
type Client interface {
	Dialer
	io.Closer
}

// Server is the tunnel server based on stream connections
type Server interface {
	Listener
	io.Closer
}

// Tunnel describes a tunnel, allowing creating a tunnel from another tunnel
// We assume that the lower tunnels know exatly how upper tunnels work, and lower tunnels is transparent for the upper tunnels
type Tunnel interface {
	Name() string
	NewClient(context.Context, Client) (Client, error)
	NewServer(context.Context, Server) (Server, error)
}

var (
	tunnels   = make(map[string]Tunnel)
	tunnelsMu sync.RWMutex
)

// RegisterTunnel register a tunnel by tunnel name
func RegisterTunnel(name string, tunnel Tunnel) {
	tunnelsMu.Lock()
	tunnels[name] = tunnel
	tunnelsMu.Unlock()
}

func GetTunnel(name string) (Tunnel, error) {
	tunnelsMu.RLock()
	defer tunnelsMu.RUnlock()
	if t, ok := tunnels[name]; ok {
		return t, nil
	}
	return nil, common.NewError("unknown tunnel name " + name)
}
