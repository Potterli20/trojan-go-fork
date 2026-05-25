package websocket

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	log.Debug("[WebSocket Client] ========== WebSocket DialConn Start ==========")
	log.Debug("[WebSocket Client] Hostname:", c.hostname)
	log.Debug("[WebSocket Client] Path:", c.path)
	log.Debug("[WebSocket Client] Number of custom headers:", len(c.headers))

	log.Debug("[WebSocket Client] Step 1: Dialing underlying connection...")
	startTime := time.Now()
	conn, err := c.underlay.DialConn(nil, &Tunnel{})
	dialDuration := time.Since(startTime)
	if err != nil {
		log.Error("[WebSocket Client] Failed to dial underlying connection after", dialDuration, ":", err)
		return nil, common.NewError("websocket cannot dial with underlying client").Base(err)
	}
	log.Info("[WebSocket Client] Underlying connection established in", dialDuration)

	url := "wss://" + c.hostname + c.path
	origin := "https://" + c.hostname
	log.Debug("[WebSocket Client] WebSocket URL:", url)
	log.Debug("[WebSocket Client] WebSocket Origin:", origin)

	log.Debug("[WebSocket Client] Step 2: Creating WebSocket configuration...")
	wsConfig, err := websocket.NewConfig(url, origin)
	if err != nil {
		log.Error("[WebSocket Client] Failed to create WebSocket config:", err)
		return nil, common.NewError("invalid websocket config").Base(err)
	}

	for key, value := range c.headers {
		wsConfig.Header.Set(key, value)
		log.Debug("websocket custom header:", key, "=", value)
	}

	handshakeStart := time.Now()
	wsConn, err := websocket.NewClient(wsConfig, conn)
	handshakeDuration := time.Since(handshakeStart)
	if err != nil {
		log.Error("[WebSocket Client] WebSocket handshake failed after", handshakeDuration, ":", err)
		conn.Close()
		return nil, common.NewError("websocket failed to handshake with server").Base(err)
	}

	log.Info("[WebSocket Client] WebSocket handshake succeeded in", handshakeDuration)
	log.Debug("[WebSocket Client] WebSocket Request:", "GET", wsConfig.Location.String())
	log.Debug("[WebSocket Client] ========== WebSocket DialConn End ==========")

	return &OutboundConn{
		Conn:    wsConn,
		tcpConn: conn,
	}, nil
}

func (c *Client) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	log.Warn("[WebSocket Client] DialPacket is not supported by WebSocket")
	return nil, common.NewError("not supported by websocket")
}

func (c *Client) Close() error {
	log.Info("[WebSocket Client] Closing WebSocket client")
	if err := c.underlay.Close(); err != nil {
		log.Error("[WebSocket Client] Failed to close underlay:", err)
		return err
	}
	log.Info("[WebSocket Client] WebSocket client closed successfully")
	return nil
}

func NewClient(ctx context.Context, underlay tunnel.Client) (*Client, error) {
	cfg := config.FromContext(ctx, Name).(*Config)

	log.Info("[WebSocket Client] ========== Creating WebSocket Client ==========")
	log.Debug("[WebSocket Client] RemoteHost:", cfg.RemoteHost)
	log.Debug("[WebSocket Client] RemotePort:", cfg.RemotePort)
	log.Debug("[WebSocket Client] WebSocket Enabled:", cfg.Websocket.Enabled)
	log.Debug("[WebSocket Client] WebSocket Host:", cfg.Websocket.Host)
	log.Debug("[WebSocket Client] WebSocket Path:", cfg.Websocket.Path)
	log.Debug("[WebSocket Client] WebSocket Headers:", cfg.Websocket.Headers)

	if !strings.HasPrefix(cfg.Websocket.Path, "/") {
		errMsg := fmt.Sprintf("websocket path must start with '/' but got '%s'", cfg.Websocket.Path)
		log.Error("[WebSocket Client]", errMsg)
		return nil, common.NewError(errMsg)
	}

	if cfg.Websocket.Host == "" {
		cfg.Websocket.Host = cfg.RemoteHost
		log.Warn("[WebSocket Client] WebSocket hostname is empty, using remote_addr:", cfg.Websocket.Host)
	} else {
		log.Debug("[WebSocket Client] Using configured WebSocket hostname:", cfg.Websocket.Host)
	}

	if len(cfg.Websocket.Headers) > 0 {
		log.Info("[WebSocket Client] Custom headers configured:")
		for key, value := range cfg.Websocket.Headers {
			log.Debug("[WebSocket Client]   ", key, ":", value)
		}
	} else {
		log.Debug("[WebSocket Client] No custom headers configured")
	}

	log.Info("[WebSocket Client] ========== WebSocket Client Created Successfully ==========")
	return &Client{
		hostname: cfg.Websocket.Host,
		path:     cfg.Websocket.Path,
		headers:  cfg.Websocket.Headers,
		underlay: underlay,
	}, nil
}
