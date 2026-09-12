package freedom

import (
	"context"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

const Name = "FREEDOM"

type Tunnel struct{}

func (*Tunnel) Name() string {
	return Name
}

func (*Tunnel) NewClient(ctx context.Context, client tunnel.Client) (tunnel.Client, error) {
	return NewClient(ctx, client)
}

func (*Tunnel) NewServer(ctx context.Context, client tunnel.Server) (tunnel.Server, error) {
	return nil, common.NewError("freedom does not support server mode")
}

func init() {
	tunnel.RegisterTunnel(Name, &Tunnel{})
}
