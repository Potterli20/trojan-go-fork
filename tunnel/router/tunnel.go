package router

import (
	"context"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

const Name = "ROUTER"

type Tunnel struct{}

func (t *Tunnel) Name() string {
	return Name
}

func (t *Tunnel) NewClient(ctx context.Context, client tunnel.Client) (tunnel.Client, error) {
	return NewClient(ctx, client)
}

func (t *Tunnel) NewServer(ctx context.Context, server tunnel.Server) (tunnel.Server, error) {
	panic("not supported")
}

func init() {
	tunnel.RegisterTunnel(Name, &Tunnel{})
}
