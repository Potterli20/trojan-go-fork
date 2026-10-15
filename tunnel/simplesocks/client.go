package simplesocks

import (
	"context"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

const (
	Connect   tunnel.Command = 1
	Associate tunnel.Command = 3
)

type Client struct {
	underlay tunnel.Client
}

func (c *Client) DialConn(addr *tunnel.Address, t tunnel.Tunnel) (tunnel.Conn, error) {
	conn, err := c.underlay.DialConn(nil, &Tunnel{})
	if err != nil {
		return nil, common.NewError("simplesocks failed to dial using underlying tunnel").Base(err)
	}
	simplesocks := &Conn{
		Conn:       conn,
		isOutbound: true,
		metadata: &tunnel.Metadata{
			Command: Connect,
			Address: addr,
		},
	}
	_, err = simplesocks.Write(make([]byte, 0)) // send simplesocks header, let the server connect to the destination
	if err != nil {
		conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		return nil, common.NewError("simplesocks failed to send header").Base(err)
	}

	return simplesocks, nil
}

func (c *Client) DialPacket(t tunnel.Tunnel) (tunnel.PacketConn, error) {
	conn, err := c.underlay.DialConn(nil, &Tunnel{})
	if err != nil {
		return nil, common.NewError("simplesocks failed to dial using underlying tunnel").Base(err)
	}
	metadata := &tunnel.Metadata{
		Command: Associate,
		Address: &tunnel.Address{
			DomainName:  "UDP_CONN",
			AddressType: tunnel.DomainName,
		},
	}
	// 走 AppendTo 而不是 WriteTo：后者要先把 Command+地址攒进一个临时切片再喂给
	// io.Writer，而这里 conn.Write 本来就是一次成型的整帧写入。
	// 顺带甩掉一个副作用——Metadata.WriteTo 会就地把自己的 Address.NetworkType 改成
	// "tcp"，这个 metadata 是即用即弃的临时对象，改不改都没人读，但少一处隐式写总是干净的。
	frame, err := metadata.AppendTo(nil)
	if err != nil {
		conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		return nil, common.NewError("simplesocks failed to serialize udp associate").Base(err)
	}
	if _, err = conn.Write(frame); err != nil {
		conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		return nil, common.NewError("simplesocks failed to write udp associate").Base(err)
	}
	return &PacketConn{
		Conn: conn,
	}, nil
}

func (c *Client) Close() error {
	return c.underlay.Close()
}

func NewClient(ctx context.Context, underlay tunnel.Client) (*Client, error) {
	log.Debug("simplesocks client created")
	return &Client{
		underlay: underlay,
	}, nil
}
