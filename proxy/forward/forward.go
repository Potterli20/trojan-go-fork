package forward

import (
	"context"

	"github.com/Potterli20/trojan-go/config"
	"github.com/Potterli20/trojan-go/proxy"
	"github.com/Potterli20/trojan-go/proxy/client"
	"github.com/Potterli20/trojan-go/tunnel"
	"github.com/Potterli20/trojan-go/tunnel/dokodemo"
)

const Name = "FORWARD"

func init() {
	proxy.RegisterProxyCreator(Name, func(ctx context.Context) (*proxy.Proxy, error) {
		cfg := config.FromContext(ctx, Name).(*client.Config)
		ctx, cancel := context.WithCancel(ctx)
		serverStack := []string{dokodemo.Name}
		clientStack := client.GenerateClientTree(cfg.TransportPlugin.Enabled, cfg.Mux.Enabled, cfg.Websocket.Enabled, cfg.Shadowsocks.Enabled, cfg.Router.Enabled)
		c, err := proxy.CreateClientStack(ctx, clientStack)
		if err != nil {
			cancel()
			return nil, err
		}
		s, err := proxy.CreateServerStack(ctx, serverStack)
		if err != nil {
			cancel()
			return nil, err
		}
		return proxy.NewProxy(ctx, cancel, []tunnel.Server{s}, c), nil
	})
}

func init() {
	config.RegisterConfigCreator(Name, func() any {
		return new(client.Config)
	})
}
