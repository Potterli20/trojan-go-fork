package shadowsocks

import (
	"context"
	"sync"

	"github.com/Potterli20/go-shadowsocks2/core"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type Client struct {
	underlay tunnel.Client
	core.Cipher
	// 幂等闸：proxy/stack.go 明确共享下层会被重复命中、依赖各 tunnel 的幂等
	// Close。本层 Close 只有一句 underlay.Close，无闸时并发/重复调用会把
	// 同一下层关多次。
	closeOnce sync.Once
	closeErr  error
}

func (c *Client) DialConn(address *tunnel.Address, tunnel tunnel.Tunnel) (tunnel.Conn, error) {
	conn, err := c.underlay.DialConn(address, &Tunnel{})
	if err != nil {
		return nil, err
	}
	return &Conn{
		aeadConn: c.Cipher.StreamConn(conn),
		Conn:     conn,
	}, nil
}

func (c *Client) DialPacket(tunnel tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, common.NewError("shadowsocks does not support packet dial")
}

func (c *Client) Close() error {
	c.closeOnce.Do(func() { c.closeErr = c.underlay.Close() })
	return c.closeErr
}

func NewClient(ctx context.Context, underlay tunnel.Client) (*Client, error) {
	cfg := config.FromContext(ctx, Name).(*Config)
	cipher, err := core.PickCipher(cfg.Shadowsocks.Method, nil, cfg.Shadowsocks.Password)
	if err != nil {
		return nil, common.NewError("invalid shadowsocks cipher").Base(err)
	}
	log.Debug("shadowsocks client created")
	return &Client{
		underlay: underlay,
		Cipher:   cipher,
	}, nil
}
