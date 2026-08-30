package server

import (
	"context"

	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/proxy"
	"github.com/Potterli20/trojan-go-fork/proxy/client"
	"github.com/Potterli20/trojan-go-fork/tunnel/freedom"
	"github.com/Potterli20/trojan-go-fork/tunnel/mux"
	"github.com/Potterli20/trojan-go-fork/tunnel/router"
	"github.com/Potterli20/trojan-go-fork/tunnel/shadowsocks"
	"github.com/Potterli20/trojan-go-fork/tunnel/simplesocks"
	"github.com/Potterli20/trojan-go-fork/tunnel/tls"
	"github.com/Potterli20/trojan-go-fork/tunnel/transport"
	"github.com/Potterli20/trojan-go-fork/tunnel/trojan"
	"github.com/Potterli20/trojan-go-fork/tunnel/websocket"
)

const Name = "SERVER"

func init() {
	proxy.RegisterProxyCreator(Name, func(ctx context.Context) (*proxy.Proxy, error) {
		cfg := config.FromContext(ctx, Name).(*client.Config)
		ctx, cancel := context.WithCancel(ctx)
		transportServer, err := transport.NewServer(ctx, nil)
		if err != nil {
			cancel()
			return nil, err
		}
		clientStack := []string{freedom.Name}
		if cfg.Router.Enabled {
			clientStack = []string{freedom.Name, router.Name}
		}

		root := &proxy.Node{
			Name:       transport.Name,
			Next:       make(map[string]*proxy.Node),
			IsEndpoint: false,
			Context:    ctx,
			Server:     transportServer,
		}

		if !cfg.TransportPlugin.Enabled {
			var err error
			root, err = root.BuildNext(tls.Name)
			if err != nil {
				cancel()
				return nil, err
			}
		}

		trojanSubTree := root
		if cfg.Shadowsocks.Enabled {
			var err error
			trojanSubTree, err = trojanSubTree.BuildNext(shadowsocks.Name)
			if err != nil {
				cancel()
				return nil, err
			}
		}
		// mux 端点与 trojan 端点都要标记 IsEndpoint:FindAllEndpoints 需同时收录
		// 自身与子树端点,否则 mux 连接会滞留在 simplesocks 的 connChan 中
		simplesocksNode, err := trojanSubTree.BuildChain(trojan.Name, mux.Name, simplesocks.Name)
		if err != nil {
			cancel()
			return nil, err
		}
		simplesocksNode.IsEndpoint = true
		trojanNode, err := trojanSubTree.BuildNext(trojan.Name)
		if err != nil {
			cancel()
			return nil, err
		}
		trojanNode.IsEndpoint = true

		wsSubTree, err := root.BuildNext(websocket.Name)
		if err != nil {
			cancel()
			return nil, err
		}
		if cfg.Shadowsocks.Enabled {
			wsSubTree, err = wsSubTree.BuildNext(shadowsocks.Name)
			if err != nil {
				cancel()
				return nil, err
			}
		}
		wsSimplesocksNode, err := wsSubTree.BuildChain(trojan.Name, mux.Name, simplesocks.Name)
		if err != nil {
			cancel()
			return nil, err
		}
		wsSimplesocksNode.IsEndpoint = true
		wsTrojanNode, err := wsSubTree.BuildNext(trojan.Name)
		if err != nil {
			cancel()
			return nil, err
		}
		wsTrojanNode.IsEndpoint = true

		serverList := proxy.FindAllEndpoints(root)
		clientList, err := proxy.CreateClientStack(ctx, clientStack)
		if err != nil {
			cancel()
			return nil, err
		}
		return proxy.NewProxy(ctx, cancel, serverList, clientList), nil
	})
}
