package http

import (
	"context"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

const Name = "HTTP"

type Tunnel struct{}

func (t *Tunnel) Name() string {
	return Name
}

func (t *Tunnel) NewClient(ctx context.Context, client tunnel.Client) (tunnel.Client, error) {
	// TODO: 未来可能支持 HTTP/2 Client 模式
	return nil, common.NewError("http tunnel does not support client mode")
}

func (t *Tunnel) NewServer(ctx context.Context, server tunnel.Server) (tunnel.Server, error) {
	// HTTP 从未注册 config creator，所以这里不读 ctx 配置。曾经对它做裸类型断言
	// （config.FromContext(ctx, Name).(*HTTP2Config)），恒为 nil ⇒ 建栈阶段直接 panic，
	// 而 http 在所有 client 模式的入站栈里，等于整个客户端起不来。
	return NewServer(ctx, server)
}

func init() {
	tunnel.RegisterTunnel(Name, &Tunnel{})
}
