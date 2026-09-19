package http

import (
	"context"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

const Name = "HTTP"

type Tunnel struct{}

// HTTP2Config HTTP/2 配置
type HTTP2Config struct {
	Enabled bool `json:"enabled" yaml:"enabled"` // 是否启用 HTTP/2
}

func (t *Tunnel) Name() string {
	return Name
}

func (t *Tunnel) NewClient(ctx context.Context, client tunnel.Client) (tunnel.Client, error) {
	// TODO: 未来可能支持 HTTP/2 Client 模式
	return nil, common.NewError("http tunnel does not support client mode")
}

func (t *Tunnel) NewServer(ctx context.Context, server tunnel.Server) (tunnel.Server, error) {
	http2Conf := config.FromContext(ctx, Name).(*HTTP2Config)
	if http2Conf == nil {
		http2Conf = &HTTP2Config{Enabled: false}
	}
	return NewServerWithHTTP2(ctx, server, http2Conf)
}

func init() {
	tunnel.RegisterTunnel(Name, &Tunnel{})
}
