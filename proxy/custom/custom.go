package custom

import (
	"context"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/proxy"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

func buildNodes(ctx context.Context, nodeConfigList []NodeConfig) (map[string]*proxy.Node, error) {
	nodes := make(map[string]*proxy.Node)
	for _, nodeCfg := range nodeConfigList {
		nodeCfg.Protocol = strings.ToUpper(nodeCfg.Protocol)
		if _, err := tunnel.GetTunnel(nodeCfg.Protocol); err != nil {
			return nil, common.NewError("invalid protocol name:" + nodeCfg.Protocol)
		}
		data, err := yaml.Marshal(nodeCfg.Config)
		if err != nil {
			return nil, common.NewError("failed to marshal node config for " + nodeCfg.Tag).Base(err)
		}
		nodeContext, err := config.WithYAMLConfig(ctx, data)
		if err != nil {
			return nil, common.NewError("failed to parse config data for " + nodeCfg.Tag + " with protocol" + nodeCfg.Protocol).Base(err)
		}
		node := &proxy.Node{
			Name:    nodeCfg.Protocol,
			Next:    make(map[string]*proxy.Node),
			Context: nodeContext,
		}
		nodes[nodeCfg.Tag] = node
	}
	return nodes, nil
}

func init() {
	proxy.RegisterProxyCreator(Name, func(ctx context.Context) (*proxy.Proxy, error) {
		cfg := config.FromContext(ctx, Name).(*Config)

		ctx, cancel := context.WithCancel(ctx)
		success := false
		// root 与 client 提前声明，失败时的统一回收才能看到这个 defer
		var root *proxy.Node
		var client tunnel.Client
		defer func() {
			if !success {
				// cancel() 不关闭任何东西：custom 模式下入站树可能已经绑好监听、
				// 出站链可能已经建好，失败时不显式回收就会每来一次配置错误就漏一份
				// fd + goroutine（root 的 CloseAll 与 client 的 Close 都会级联）
				if root != nil {
					root.CloseAll()
				}
				if client != nil {
					client.Close() //gosec:disable -- 错误忽略：失败回收路径
				}
				cancel()
			}
		}()
		// inbound
		nodes, err := buildNodes(ctx, cfg.Inbound.Node)
		if err != nil {
			return nil, err
		}

		// build server tree（root 已在前面声明，失败回收的 defer 需要看得到它）
		for _, path := range cfg.Inbound.Path {
			var lastNode *proxy.Node
			for _, tag := range path {
				if _, found := nodes[tag]; !found {
					return nil, common.NewError("invalid node tag: " + tag)
				}
				if lastNode == nil {
					if root == nil {
						lastNode = nodes[tag]
						root = lastNode
						t, err := tunnel.GetTunnel(root.Name)
						if err != nil {
							return nil, common.NewError("failed to find root tunnel").Base(err)
						}
						s, err := t.NewServer(root.Context, nil)
						if err != nil {
							return nil, common.NewError("failed to init root server").Base(err)
						}
						root.Server = s
					} else {
						lastNode = root
					}
				} else {
					var err error
					lastNode, err = lastNode.LinkNextNode(nodes[tag])
					if err != nil {
						return nil, common.NewError("failed to link node " + tag).Base(err)
					}
				}
			}
			lastNode.IsEndpoint = true
		}

		servers := proxy.FindAllEndpoints(root)

		if len(cfg.Outbound.Path) != 1 {
			return nil, common.NewError("there must be only 1 path for outbound protocol stack")
		}

		// outbound
		nodes, err = buildNodes(ctx, cfg.Outbound.Node)
		if err != nil {
			return nil, err
		}

		// build client stack
		for _, tag := range cfg.Outbound.Path[0] {
			if _, found := nodes[tag]; !found {
				return nil, common.NewError("invalid node tag: " + tag)
			}
			t, err := tunnel.GetTunnel(nodes[tag].Name)
			if err != nil {
				return nil, common.NewError("invalid tunnel name").Base(err)
			}
			// 先接 next 再赋值：出错时原先的写法会把已建好的内层链覆盖成 nil，
			// 之后谁也关不掉它
			next, err := t.NewClient(nodes[tag].Context, client)
			if err != nil {
				return nil, common.NewError("failed to create client").Base(err)
			}
			client = next
		}

		success = true
		return proxy.NewProxy(ctx, cancel, servers, client), nil
	})
}
