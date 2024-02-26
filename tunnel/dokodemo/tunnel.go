package dokodemo

import (
	"context"

	"gitlab.atcatw.org/atca/community-edition/trojan-go.git/common"
	"gitlab.atcatw.org/atca/community-edition/trojan-go.git/tunnel"
)

const Name = "DOKODEMO"

type Tunnel struct{ tunnel.Tunnel }

func (*Tunnel) Name() string {
	return Name
}

func (*Tunnel) NewServer(ctx context.Context, underlay tunnel.Server) (tunnel.Server, error) {
	return NewServer(ctx, underlay)
}

func (*Tunnel) NewClient(ctx context.Context, underlay tunnel.Client) (tunnel.Client, error) {
	return nil, common.NewError("not supported")
}

func init() {
	tunnel.RegisterTunnel(Name, &Tunnel{})
}
