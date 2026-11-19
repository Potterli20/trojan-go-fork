package custom

import (
	"strings"
	"testing"

	"github.com/Potterli20/trojan-go-fork/proxy"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/transport" // 注册 transport
	_ "github.com/Potterli20/trojan-go-fork/tunnel/freedom"  // 注册 freedom（出站根）
)

// TestSelfBindingLayerMustBeInboundRoot 钉住入站 path 的自绑层位置约束：
// 自绑层（TRANSPORT/QUIC 等）的 NewServer 忽略传入的 underlay、自己另开监听。
// 若把它链在非首节点（如 [transport, transport]），父层 transport 绑的监听端口
// 虽有连接进来，却因父层不是 endpoint 而没有 relay 消费——连接悬挂、fd/协程堆积，
// 全程无报错，是典型的"接受但不生效"。应在配置层拒绝：自绑层只能作 path 首节点。
//
// 变异对照：去掉非首节点的自绑层校验，本用例转红（建栈成功）。
func TestSelfBindingLayerMustBeInboundRoot(t *testing.T) {
	cfg := `{
		"run_type":"custom",
		"inbound":{
			"node":[
				{"protocol":"transport","tag":"outer","config":{"local_addr":"127.0.0.1","local_port":12091,"remote_addr":"127.0.0.1","remote_port":12092}},
				{"protocol":"transport","tag":"inner","config":{"local_addr":"127.0.0.1","local_port":12093,"remote_addr":"127.0.0.1","remote_port":12094}}
			],
			"path":[["outer","inner"]]
		},
		"outbound":{
			"node":[{"protocol":"freedom","tag":"out","config":{}}],
			"path":[["out"]]
		}
	}`
	p, err := proxy.NewProxyFromConfigData([]byte(cfg), true)
	if err == nil {
		if p != nil {
			p.Close() //gosec:disable -- 错误忽略：测试清理
		}
		t.Fatal("自绑层 transport 链在非首节点应报错，而不是建栈成功（父层监听会无人 relay）")
	}
	if !strings.Contains(err.Error(), "root") && !strings.Contains(err.Error(), "self") {
		t.Fatalf("错误信息应指出自绑层只能作根节点：%v", err)
	}
}

// TestSelfBindingRootAloneStillWorks 是正向对照：自绑层单独作根（path 只有一个节点）
// 是合法且常见的栈，不能被这次位置校验误拒。
func TestSelfBindingRootAloneStillWorks(t *testing.T) {
	cfg := `{
		"run_type":"custom",
		"inbound":{
			"node":[{"protocol":"transport","tag":"root","config":{"local_addr":"127.0.0.1","local_port":12095,"remote_addr":"127.0.0.1","remote_port":12096}}],
			"path":[["root"]]
		},
		"outbound":{
			"node":[{"protocol":"freedom","tag":"out","config":{}}],
			"path":[["out"]]
		}
	}`
	p, err := proxy.NewProxyFromConfigData([]byte(cfg), true)
	if err != nil {
		t.Fatalf("自绑层单独作根应被允许：%v", err)
	}
	if p != nil {
		p.Close() //gosec:disable -- 错误忽略：测试清理
	}
}
