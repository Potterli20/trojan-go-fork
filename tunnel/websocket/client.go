package websocket

import (
	"context"
	"strings"
	"net"
	"golang.org/x/net/websocket"
	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type Client struct {
	underlay tunnel.Client
	hostname string
	path     string
}

func (c *Client) DialConn(addr *tunnel.Address, t tunnel.Tunnel) (tunnel.Conn, error) {
	conn, err := c.underlay.DialConn(nil, &Tunnel{})
	if err != nil {
		return nil, common.NewError("WebSocket cannot dial with underlying client").Base(err)
	}
	url := "wss://" + c.hostname + c.path
	origin := "https://" + c.hostname
	wsConfig, err := websocket.NewConfig(url, origin)
	if err != nil {
		return nil, common.NewError("Invalid WebSocket config").Base(err)
	}
	wsConn, err := websocket.NewClient(wsConfig, conn)
	if err != nil {
		return nil, common.NewError("WebSocket failed to handshake with server").Base(err)
	}
	return &OutboundConn{
		Conn:    wsConn,
		tcpConn: conn,
	}, nil
}

func (c *Client) DialPacket(t tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, common.NewError("Not supported by WebSocket")
}

func (c *Client) Close() error {
	return c.underlay.Close()
}

func NewClient(ctx context.Context, underlay tunnel.Client) (*Client, error) {
	cfg := config.FromContext(ctx, "websocket").(*Config)
	if !strings.HasPrefix(cfg.Websocket.Path, "/") {
		return nil, common.NewError("WebSocket path must start with \"/\"")
	}
	if cfg.Websocket.Host == "" {
		cfg.Websocket.Host = cfg.RemoteHost
		log.Warn("Empty WebSocket hostname")
	}
	log.Debug("WebSocket client created")
	return &Client{
		hostname: cfg.Websocket.Host,
		path:     cfg.Websocket.Path,
		underlay: underlay,
	}, nil
}
