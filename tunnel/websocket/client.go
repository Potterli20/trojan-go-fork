package websocket

import (
	"context"
	"fmt"
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
	if log.ShouldLog(log.DebugLevel) {
		log.Debug("[WebSocket] DialConn start - hostname:", c.hostname, "path:", c.path, "headers:", len(c.headers))
	}

	tracker := log.NewConnectionTracker("WebSocket", "DialConn").
		WithField("hostname", c.hostname).
		WithField("path", c.path)

	conn, err := c.underlay.DialConn(nil, &Tunnel{})
	if err != nil {
		tracker.Error(err)
		return nil, common.NewError("websocket cannot dial with underlying client").Base(err)
	}

	url := "wss://" + c.hostname + c.path
	if log.ShouldLog(log.DebugLevel) {
		log.Debug("[WebSocket] URL:", url)
	}

	wsConfig, err := websocket.NewConfig(url, "https://"+c.hostname)
	if err != nil {
		log.Error("[WebSocket] Failed to create config:", err)
		return nil, common.NewError("invalid websocket config").Base(err)
	}

	for key, value := range c.headers {
		wsConfig.Header.Set(key, value)
		if log.ShouldLog(log.DebugLevel) {
			log.Debug("[WebSocket] Custom header:", key, "=", value)
		}
	}

	wsConn, err := websocket.NewClient(wsConfig, conn)
	if err != nil {
		log.Error("[WebSocket] Handshake failed:", err)
		conn.Close()
		return nil, common.NewError("websocket failed to handshake with server").Base(err)
	}

	tracker.Success()
	return &OutboundConn{
		Conn:    wsConn,
		tcpConn: conn,
	}, nil
}

func (c *Client) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	log.Warn("[WebSocket] DialPacket is not supported")
	return nil, common.NewError("not supported by websocket")
}

func (c *Client) Close() error {
	log.Info("[WebSocket] Closing client")
	if err := c.underlay.Close(); err != nil {
		log.Error("[WebSocket] Failed to close underlay:", err)
		return err
	}
	log.Info("[WebSocket] Client closed successfully")
	return nil
}

func NewClient(ctx context.Context, underlay tunnel.Client) (*Client, error) {
	cfg := config.FromContext(ctx, Name).(*Config)

	log.Info("[WebSocket] Creating client")
	if log.ShouldLog(log.DebugLevel) {
		log.Debug("[WebSocket] RemoteHost:", cfg.RemoteHost)
		log.Debug("[WebSocket] RemotePort:", cfg.RemotePort)
		log.Debug("[WebSocket] Enabled:", cfg.Websocket.Enabled)
		log.Debug("[WebSocket] Host:", cfg.Websocket.Host)
		log.Debug("[WebSocket] Path:", cfg.Websocket.Path)
		log.Debug("[WebSocket] Headers:", cfg.Websocket.Headers)
	}

	if !strings.HasPrefix(cfg.Websocket.Path, "/") {
		errMsg := fmt.Sprintf("websocket path must start with '/' but got '%s'", cfg.Websocket.Path)
		log.Error("[WebSocket]", errMsg)
		return nil, common.NewError(errMsg)
	}

	if cfg.Websocket.Host == "" {
		cfg.Websocket.Host = cfg.RemoteHost
		log.Warn("[WebSocket] Hostname is empty, using remote_addr:", cfg.Websocket.Host)
	} else if log.ShouldLog(log.DebugLevel) {
		log.Debug("[WebSocket] Using configured hostname:", cfg.Websocket.Host)
	}

	if len(cfg.Websocket.Headers) > 0 {
		log.Info("[WebSocket] Custom headers configured:")
		if log.ShouldLog(log.DebugLevel) {
			for key, value := range cfg.Websocket.Headers {
				log.Debug("[WebSocket]   ", key, ":", value)
			}
		}
	} else if log.ShouldLog(log.DebugLevel) {
		log.Debug("[WebSocket] No custom headers configured")
	}

	log.Info("[WebSocket] Client created successfully")
	return &Client{
		hostname: cfg.Websocket.Host,
		path:     cfg.Websocket.Path,
		headers:  cfg.Websocket.Headers,
		underlay: underlay,
	}, nil
}
