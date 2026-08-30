package websocket

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// clientHandshakeTimeout 限定客户端等待服务器升级响应的时限
const clientHandshakeTimeout = 30 * time.Second

type Client struct {
	underlay tunnel.Client
	hostname string
	path     string
	headers  map[string]string
}

func (c *Client) DialConn(*tunnel.Address, tunnel.Tunnel) (tunnel.Conn, error) {
	tracker := log.NewConnectionTracker("WebSocket", "DialConn").
		WithField("hostname", c.hostname).
		WithField("path", c.path)

	log.Debugf("[WebSocket] [conn=%s] Dialing to %s%s, headers=%d",
		tracker.ConnID(), c.hostname, c.path, len(c.headers))

	conn, err := c.underlay.DialConn(nil, &Tunnel{})
	if err != nil {
		_ = tracker.Error(err)
		return nil, common.NewError("websocket cannot dial with underlying client").Base(err)
	}

	// TLS 由上层隧道完成,此处用 ws:// 占位;coder 会把请求经固定返回底层连接的
	// Transport 发出,从而在已建立的连接上完成 RFC 6455 升级握手,不再拨号或 TLS
	host := c.hostname
	dialHeader := make(http.Header)
	for key, value := range c.headers {
		if strings.EqualFold(key, "Host") {
			host = value
			continue
		}
		dialHeader.Set(key, value)
		if log.ShouldLog(log.DebugLevel) {
			log.Debug("[WebSocket] Custom header:", key, "=", value)
		}
	}

	url := "ws://" + c.hostname + c.path
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		conn.Close()
		return nil, common.NewError("invalid websocket url").Base(err)
	}
	request.Host = host
	request.Header = dialHeader

	dialCtx, cancel := context.WithTimeout(context.Background(), clientHandshakeTimeout)
	defer cancel()
	wsConn, resp, err := websocket.Dial(dialCtx, url, &websocket.DialOptions{
		Host:       host,
		HTTPHeader: dialHeader,
		HTTPClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(context.Context, string, string) (net.Conn, error) {
					return conn, nil
				},
			},
		},
	})
	if err != nil {
		_ = tracker.Error(err)
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		conn.Close()
		log.Errorf("[WebSocket] [conn=%s] Handshake failed: %v", tracker.ConnID(), err)
		return nil, common.NewError("websocket failed to handshake with server").Base(err)
	}
	// NetConn 会把读上限重置为 -1,必须在 NetConn 之后重新设置
	stream := websocket.NetConn(context.Background(), wsConn, websocket.MessageBinary)
	wsConn.SetReadLimit(maxMessageSize)

	_ = tracker.Success()
	log.Debugf("[WebSocket] [conn=%s] Connection established successfully", tracker.ConnID())
	return &OutboundConn{
		Conn:    stream,
		tcpConn: conn,
		request: request,
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
