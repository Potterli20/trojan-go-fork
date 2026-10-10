package proxy

import (
	"context"

	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type Node struct {
	Name       string
	Next       map[string]*Node
	IsEndpoint bool
	context.Context
	tunnel.Server
	tunnel.Client
}

func (n *Node) BuildNext(name string) (*Node, error) {
	if next, found := n.Next[name]; found {
		return next, nil
	}
	t, err := tunnel.GetTunnel(name)
	if err != nil {
		return nil, err
	}
	s, err := t.NewServer(n.Context, n.Server)
	if err != nil {
		return nil, err
	}
	newNode := &Node{
		Name:    name,
		Next:    make(map[string]*Node),
		Context: n.Context,
		Server:  s,
	}
	n.Next[name] = newNode
	return newNode, nil
}

func (n *Node) LinkNextNode(next *Node) (*Node, error) {
	if found, ok := n.Next[next.Name]; ok {
		return found, nil
	}
	n.Next[next.Name] = next
	t, err := tunnel.GetTunnel(next.Name)
	if err != nil {
		delete(n.Next, next.Name)
		return nil, err
	}
	s, err := t.NewServer(next.Context, n.Server) // context of the child nodes have been initialized
	if err != nil {
		// 回滚链接,避免失败的半初始化节点被 FindAllEndpoints 触达
		delete(n.Next, next.Name)
		return nil, err
	}
	next.Server = s
	return next, nil
}

// BuildChain 从 n 出发依次 BuildNext,返回链条末端节点
func (n *Node) BuildChain(names ...string) (*Node, error) {
	current := n
	for _, name := range names {
		next, err := current.BuildNext(name)
		if err != nil {
			return nil, err
		}
		current = next
	}
	return current, nil
}

func FindAllEndpoints(root *Node) []tunnel.Server {
	// 注意：不能用 early-return。trojan 节点可能同时被标记为 IsEndpoint 且拥有
	// mux→simplesocks 子节点（服务端树构建对同一父节点两次 BuildNext(trojan) 会复用
	// 同一节点），此时必须把自身和子树端点都收录，否则 mux 端点没有 relay goroutine，
	// 所有 mux 连接（TCP 与 WebSocket）会永久滞留在 simplesocks 的 connChan 中。
	list := make([]tunnel.Server, 0)
	if root.IsEndpoint || len(root.Next) == 0 {
		list = append(list, root.Server)
	}
	for _, next := range root.Next {
		list = append(list, FindAllEndpoints(next)...)
	}
	return list
}

// CreateClientStack create client tunnel stacks from lists
func CreateClientStack(ctx context.Context, clientStack []string) (tunnel.Client, error) {
	var client tunnel.Client
	for _, name := range clientStack {
		t, err := tunnel.GetTunnel(name)
		if err != nil {
			closeTunnel(client, nil)
			return nil, err
		}
		// 先接到 next 再赋值：原先写成 client, err = t.NewClient(...)，出错时
		// client 会被这次调用的 nil 覆盖掉，已经建好的内层就再也关不掉了
		next, err := t.NewClient(ctx, client)
		if err != nil {
			closeTunnel(client, nil)
			return nil, err
		}
		client = next
	}
	return client, nil
}

// CreateServerStack create server tunnel stack from list
func CreateServerStack(ctx context.Context, serverStack []string) (tunnel.Server, error) {
	var server tunnel.Server
	for _, name := range serverStack {
		t, err := tunnel.GetTunnel(name)
		if err != nil {
			closeTunnel(nil, server)
			return nil, err
		}
		next, err := t.NewServer(ctx, server)
		if err != nil {
			closeTunnel(nil, server)
			return nil, err
		}
		server = next
	}
	return server, nil
}

// closeTunnel 回收构建中途失败的 tunnel 链。栈是内层先建的，变量里存的是最外面
// 那一个，而每个 tunnel 的 Close 都会级联关自己的下层，所以关一次就覆盖整条链。
// 入站 server 会绑定监听端口，漏掉就是 fd + 端口 + 后台 goroutine 常驻；
// 重复关闭由各 tunnel 的幂等 Close 兜住（transport/adapter/quic 已处理）。
func closeTunnel(client tunnel.Client, server tunnel.Server) {
	if client != nil {
		if err := client.Close(); err != nil {
			log.Debug("failed to close the partially built client stack:", err)
		}
	}
	if server != nil {
		if err := server.Close(); err != nil {
			log.Debug("failed to close the partially built server stack:", err)
		}
	}
}

// CloseAll 关闭以 n 为根的入站树，供构建失败的回收路径使用。n 为 nil 时直接返回：
// 回收路径自己 panic 会比泄露更难查。
// cancel() 不会关闭任何已经建立的监听，所以每个提前返回都必须显式回收。
// 树里父节点会被多个子节点共用（如 client 模式的 adapter 同时是 socks 与 http 的
// 下层），FindAllEndpoints 因此可能重复命中同一个下层——这依赖各 tunnel 的幂等 Close。
func (n *Node) CloseAll() {
	if n == nil {
		return
	}
	for _, s := range FindAllEndpoints(n) {
		// 构建中途失败的节点，Server 可能还是 nil（例如 custom 模式里 root 先入树、
		// NewServer 才失败）。对 nil 接口调 Close 会 panic，把真正的配置错误吞掉。
		if s == nil {
			continue
		}
		if err := s.Close(); err != nil {
			log.Debug("failed to close a tunnel while unwinding the inbound tree:", err)
		}
	}
}
