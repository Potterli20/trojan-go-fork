package client

import (
	"context"
	"slices"

	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/proxy"
	"github.com/Potterli20/trojan-go-fork/tunnel/adapter"
	"github.com/Potterli20/trojan-go-fork/tunnel/http"
	"github.com/Potterli20/trojan-go-fork/tunnel/mux"
	"github.com/Potterli20/trojan-go-fork/tunnel/router"
	"github.com/Potterli20/trojan-go-fork/tunnel/shadowsocks"
	"github.com/Potterli20/trojan-go-fork/tunnel/simplesocks"
	"github.com/Potterli20/trojan-go-fork/tunnel/socks"
	"github.com/Potterli20/trojan-go-fork/tunnel/tls"
	"github.com/Potterli20/trojan-go-fork/tunnel/transport"
	"github.com/Potterli20/trojan-go-fork/tunnel/trojan"
	"github.com/Potterli20/trojan-go-fork/tunnel/websocket"
)

const Name = "CLIENT"

// GenerateClientTree generate general outbound protocol stack
func GenerateClientTree(transportPlugin bool, muxEnabled bool, wsEnabled bool, ssEnabled bool, routerEnabled bool) []string {
	var parts [][]string
	parts = append(parts, []string{transport.Name})
	if !transportPlugin {
		parts = append(parts, []string{tls.Name})
	}
	if wsEnabled {
		parts = append(parts, []string{websocket.Name})
	}
	if ssEnabled {
		parts = append(parts, []string{shadowsocks.Name})
	}
	parts = append(parts, []string{trojan.Name})
	if muxEnabled {
		parts = append(parts, []string{mux.Name, simplesocks.Name})
	}
	if routerEnabled {
		parts = append(parts, []string{router.Name})
	}
	return slices.Concat(parts...)
}

func init() {
	proxy.RegisterProxyCreator(Name, func(ctx context.Context) (*proxy.Proxy, error) {
		cfg := config.FromContext(ctx, Name).(*Config)
		adapterServer, err := adapter.NewServer(ctx, nil)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithCancel(ctx)

		root := &proxy.Node{
			Name:       adapter.Name,
			Next:       make(map[string]*proxy.Node),
			IsEndpoint: false,
			Context:    ctx,
			Server:     adapterServer,
		}

		root.BuildNext(http.Name).IsEndpoint = true
		root.BuildNext(socks.Name).IsEndpoint = true

		clientStack := GenerateClientTree(cfg.TransportPlugin.Enabled, cfg.Mux.Enabled, cfg.Websocket.Enabled, cfg.Shadowsocks.Enabled, cfg.Router.Enabled)
		c, err := proxy.CreateClientStack(ctx, clientStack)
		if err != nil {
			cancel()
			return nil, err
		}
		s := proxy.FindAllEndpoints(root)
		return proxy.NewProxy(ctx, cancel, s, c), nil
	})
}
