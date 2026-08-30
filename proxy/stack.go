package proxy

import (
	"context"

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
			return nil, err
		}
		client, err = t.NewClient(ctx, client)
		if err != nil {
			return nil, err
		}
	}
	return client, nil
}

// CreateServerStack create server tunnel stack from list
func CreateServerStack(ctx context.Context, serverStack []string) (tunnel.Server, error) {
	var server tunnel.Server
	for _, name := range serverStack {
		t, err := tunnel.GetTunnel(name)
		if err != nil {
			return nil, err
		}
		server, err = t.NewServer(ctx, server)
		if err != nil {
			return nil, err
		}
	}
	return server, nil
}
