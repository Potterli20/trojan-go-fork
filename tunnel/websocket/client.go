package websocket

import (
	"context"
	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"golang.org/x/net/websocket"
	"strings"
)

// Client is a WebSocket client.
type Client struct {
	underlay tunnel.Client
	hostname string
	path     string
}

// DialConn dials a connection.
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

// DialPacket dials a packet connection.
func (c *Client) DialPacket(t tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, common.NewError("Not supported by WebSocket")
}

// Close closes the WebSocket client.
func (c *Client) Close() error {
	return c.underlay.Close()
}

// NewClient creates a new WebSocket client.
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
