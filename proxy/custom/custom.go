package custom

import (
	"context"
	"encoding/json"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/proxy"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// buildNodes 用与顶层配置相同的解析器还原每个节点的 config（any 字段）。
// JSON 来源解析出下划线键（local_addr），必须走 JSON 标签；YAML 来源是 hyphen
// 键（local-addr），必须走 YAML 标签。混用会让节点参数被静默丢弃——进程照常启动，
// 用户以为配好的监听地址/端口实际是全默认。
func buildNodes(ctx context.Context, nodeConfigList []NodeConfig, isJSON bool) (map[string]*proxy.Node, error) {
	nodes := make(map[string]*proxy.Node)
	for _, nodeCfg := range nodeConfigList {
		// 重复 tag 会被 nodes[tag] = node 静默覆盖，先定义的节点连同配置一起丢掉，
		// 链路错配且无症状。在配置层拒绝，变成可读的启动错误。
		if _, exists := nodes[nodeCfg.Tag]; exists {
			return nil, common.NewError("duplicate node tag: " + nodeCfg.Tag)
		}
		nodeCfg.Protocol = strings.ToUpper(nodeCfg.Protocol)
		if _, err := tunnel.GetTunnel(nodeCfg.Protocol); err != nil {
			return nil, common.NewError("invalid protocol name:" + nodeCfg.Protocol)
		}
		var nodeContext context.Context
		var err error
		if isJSON {
			var data []byte
			data, err = json.Marshal(nodeCfg.Config)
			if err != nil {
				return nil, common.NewError("failed to marshal node config for " + nodeCfg.Tag).Base(err)
			}
			nodeContext, err = config.WithJSONConfig(ctx, data)
		} else {
			var data []byte
			data, err = yaml.Marshal(nodeCfg.Config)
			if err != nil {
				return nil, common.NewError("failed to marshal node config for " + nodeCfg.Tag).Base(err)
			}
			nodeContext, err = config.WithYAMLConfig(ctx, data)
		}
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

// rootCanStandAlone 报告某协议能否作为 custom 入站树的根节点（NewServer(ctx, nil) 合法）。
// 这些层的 NewServer 自己 Listen，不解引用传入的 underlay；其余层都需要下层。
func rootCanStandAlone(protocol string) bool {
	switch protocol {
	case "TRANSPORT", "ADAPTER", "DOKODEMO", "TPROXY", "QUIC":
		return true
	default:
		return false
	}
}

// clientCanBeInnermost 报告某协议能否作为 custom 出站链的最内层（NewClient(ctx, nil) 合法）。
// 这些层自己拨号、不解引用 underlay；其余层（trojan/tls/websocket/mux/...）在
// DialConn 时才对 underlay 解引用。出站首层配成它们时建栈照样成功，直到首个连接
// 到达才 SIGSEGV（在无 recover 的中继协程里，整个进程崩掉）——必须在配置层拒绝。
// 集合与入站 rootCanStandAlone 不同：freedom/transport 自拨号但不监听，adapter/
// dokodemo/tproxy 只监听不拨号。新增自拨号层时需同步登记到这里。
func clientCanBeInnermost(protocol string) bool {
	switch protocol {
	case "FREEDOM", "TRANSPORT", "QUIC":
		return true
	default:
		return false
	}
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
		isJSON := proxy.IsJSONConfig(ctx)
		nodes, err := buildNodes(ctx, cfg.Inbound.Node, isJSON)
		if err != nil {
			return nil, err
		}

		// build server tree（root 已在前面声明，失败回收的 defer 需要看得到它）
		for _, path := range cfg.Inbound.Path {
			var lastNode *proxy.Node
			// 同一 path 里重复出现同一个 tag 会把节点链成环（LinkNextNode 以 protocol
			// 作键，且不校验 next 是否为祖先），随后 FindAllEndpoints 对环无限递归 →
			// 栈溢出崩进程。在配置层就拒掉，把它变成一条可读的启动错误。
			seen := make(map[string]bool, len(path))
			for _, tag := range path {
				if seen[tag] {
					return nil, common.NewError("duplicate node tag in one path would create a cycle: " + tag)
				}
				seen[tag] = true
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
						// 根节点没有下层，必须是自绑监听层（自己 Listen 的入口）。
						// 把需要 underlay 的协议（socks/tls/trojan/... 等）放到根节点，
						// 其 NewServer(ctx, nil) 会解引用 nil（实测 socks.NewServer 在
						// underlay.AcceptPacket 处 SIGSEGV）——错误配置不该崩进程，
						// 在配置层挡下并给出可读错误。集合取自"NewServer 不碰 underlay"
						// 的入口层；新增自绑层时需同步登记到这里。
						if !rootCanStandAlone(root.Name) {
							return nil, common.NewError("inbound root protocol " + root.Name + " requires an underlay; use a self-listening layer (TRANSPORT/ADAPTER/DOKODEMO/TPROXY/QUIC) as the first node")
						}
						s, err := t.NewServer(root.Context, nil)
						if err != nil {
							return nil, common.NewError("failed to init root server").Base(err)
						}
						root.Server = s
					} else {
						// 只支持一棵以首个 path 的首个 tag 为根的树（FindAllEndpoints 只从
						// 一个 root 遍历）。后续 path 若以别的节点开头，静默接到 root 上会
						// 丢配置、错配链路，直接报错。
						if nodes[tag] != root {
							return nil, common.NewError("only the first path may define the root; this path starts with a different tag: " + tag)
						}
						lastNode = root
					}
				} else {
					// 自绑层（TRANSPORT/QUIC/...）的 NewServer 忽略传入的 underlay、
					// 自己另开监听。若链在非首节点，父层绑的监听端口虽有连接进来，
					// 却因父层不是 endpoint 而没有 relay 消费——连接悬挂、fd/协程
					// 堆积且无报错。自绑层只能是 path 首节点，在接线前挡下。
					if rootCanStandAlone(nodes[tag].Name) {
						return nil, common.NewError("inbound self-listening protocol " + nodes[tag].Name + " must be the first node of a path; it ignores the underlay and would leave the parent listener unconsumed")
					}
					var err error
					lastNode, err = lastNode.LinkNextNode(nodes[tag])
					if err != nil {
						return nil, common.NewError("failed to link node " + tag).Base(err)
					}
				}
			}
			if lastNode == nil {
				return nil, common.NewError("empty path entry in inbound.path")
			}
			lastNode.IsEndpoint = true
		}

		if root == nil {
			return nil, common.NewError("inbound.path is empty: nothing to serve")
		}
		servers := proxy.FindAllEndpoints(root)

		if len(cfg.Outbound.Path) != 1 {
			return nil, common.NewError("there must be only 1 path for outbound protocol stack")
		}

		// outbound
		nodes, err = buildNodes(ctx, cfg.Outbound.Node, isJSON)
		if err != nil {
			return nil, err
		}

		// build client stack
		for _, tag := range cfg.Outbound.Path[0] {
			if _, found := nodes[tag]; !found {
				return nil, common.NewError("invalid node tag: " + tag)
			}
			// 最内层（client==nil）必须是自拨号层：其余层的 NewClient 只把 nil
			// 存进 underlay、不立即解引用，建栈会"成功"，直到首个连接到达才
			// DialConn 对 nil 解引用 SIGSEGV（在无 recover 的中继协程里崩进程）。
			// 与入站 rootCanStandAlone 对称，在配置层挡下并给出可读错误。
			if client == nil {
				if !clientCanBeInnermost(nodes[tag].Name) {
					return nil, common.NewError("outbound root protocol " + nodes[tag].Name + " requires an underlay; use a self-dialing layer (FREEDOM/TRANSPORT/QUIC) as the first node")
				}
			} else if clientCanBeInnermost(nodes[tag].Name) {
				// 自拨号层忽略传入的 underlay（FREEDOM/TRANSPORT 的 NewClient 参数名
				// 是 `_`，QUIC 存了但拨号只用 quic.DialAddr）。若它出现在非最内层，
				// 内层节点被完整建好（ctx/TLS/全局出网状态/QUIC 资源）却永远不被
				// 流量经过——内层配置静默失效、资源孤立，建栈还成功、无日志。
				// 与入站 rootCanStandAlone 的"自绑层只能是首节点"那道闸对称。
				return nil, common.NewError("outbound self-dialing protocol " + nodes[tag].Name + " must be the innermost node; it ignores the underlay and would silently discard the inner stack")
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

		if client == nil {
			return nil, common.NewError("outbound.path is empty")
		}
		success = true
		return proxy.NewProxy(ctx, cancel, servers, client), nil
	})
}
