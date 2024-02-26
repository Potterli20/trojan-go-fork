package trojan

import (
	"context"

	"gitlab.atcatw.org/atca/community-edition/trojan-go.git/tunnel"
)

const Name = "TROJAN"

type Tunnel struct{}

func (c *Tunnel) Name() string {
	return Name
}

func (c *Tunnel) NewClient(ctx context.Context, client tunnel.Client) (tunnel.Client, error) {
	return NewClient(ctx, client)
}

func (c *Tunnel) NewServer(ctx context.Context, server tunnel.Server) (tunnel.Server, error) {
	return NewServer(ctx, server)
}

func init() {
	tunnel.RegisterTunnel(Name, &Tunnel{})
}
