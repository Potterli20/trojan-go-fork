package websocket

import (
	"context"
	"strings"

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
	headers  map[string]string
}

func (c *Client) DialConn(*tunnel.Address, tunnel.Tunnel) (tunnel.Conn, error) {
	conn, err := c.underlay.DialConn(nil, &Tunnel{})
	if err != nil {
		return nil, common.NewError("websocket cannot dial with underlying client").Base(err)
	}
	url := "wss://" + c.hostname + c.path
	origin := "https://" + c.hostname
	wsConfig, err := websocket.NewConfig(url, origin)
	if err != nil {
		return nil, common.NewError("invalid websocket config").Base(err)
	}

	for key, value := range c.headers {
		wsConfig.Header.Set(key, value)
		log.Debug("websocket custom header:", key, "=", value)
	}

	wsConn, err := websocket.NewClient(wsConfig, conn)
	if err != nil {
		return nil, common.NewError("websocket failed to handshake with server").Base(err)
	}
	return &OutboundConn{
		Conn:    wsConn,
		tcpConn: conn,
	}, nil
}

func (c *Client) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, common.NewError("not supported by websocket")
}

func (c *Client) Close() error {
	return c.underlay.Close()
}

func NewClient(ctx context.Context, underlay tunnel.Client) (*Client, error) {
	cfg := config.FromContext(ctx, Name).(*Config)
	if !strings.HasPrefix(cfg.Websocket.Path, "/") {
		return nil, common.NewError("websocket path must start with \"/\"")
	}
	if cfg.Websocket.Host == "" {
		cfg.Websocket.Host = cfg.RemoteHost
		log.Warn("empty websocket hostname")
	}
	log.Debug("websocket client created")
	return &Client{
		hostname: cfg.Websocket.Host,
		path:     cfg.Websocket.Path,
		headers:  cfg.Websocket.Headers,
		underlay: underlay,
	}, nil
}
