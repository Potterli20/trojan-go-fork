package custom

import (
	"strings"
	"testing"

	"github.com/Potterli20/trojan-go-fork/proxy"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/transport" // 注册 transport（自绑根节点层）
)

// TestRootServerWithoutUnderlayReturnsError 钉住 custom 根节点的防御：
// 根节点按设计必须是自绑监听层（adapter/transport/dokodemo 等），若用户把首个
// inbound 节点配成需要 underlay 的协议（如 socks），其 NewServer(ctx, nil) 会在
// socks.NewServer 里解引用 nil → SIGSEGV。错误配置应给可读错误，而不是崩进程。
// 变异对照：去掉 underlay 校验，本用例 panic 转红。
func TestRootServerWithoutUnderlayReturnsError(t *testing.T) {
	cfg := `{
		"run_type":"custom",
		"inbound":{
			"node":[{"protocol":"socks","tag":"root","config":{"local_addr":"127.0.0.1","local_port":1080}}],
			"path":[["root"]]
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
		t.Fatal("根节点配成需要 underlay 的 socks 应报错，而不是成功或 panic")
	}
	if !strings.Contains(err.Error(), "underlay") {
		t.Fatalf("错误信息应指出根节点不能是无 underlay 的协议：%v", err)
	}
}

// TestDuplicateNodeTagReturnsError 钉住 custom 节点表的唯一性校验：
// buildNodes 用 nodes[tag] = node 登记，两个节点用同一 tag 时后者静默覆盖前者，
// 先定义的节点连同它的配置被吞掉，链路错配且无症状。应在配置层拒绝。
// 变异对照：去掉重复 tag 检查，本用例转红。
func TestDuplicateNodeTagReturnsError(t *testing.T) {
	cfg := `{
		"run_type":"custom",
		"inbound":{
			"node":[
				{"protocol":"transport","tag":"dup","config":{"local_addr":"127.0.0.1","local_port":1081,"remote_addr":"127.0.0.1","remote_port":1081}},
				{"protocol":"transport","tag":"dup","config":{"local_addr":"127.0.0.1","local_port":1082,"remote_addr":"127.0.0.1","remote_port":1082}}
			],
			"path":[["dup"]]
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
		t.Fatal("重复节点 tag 应报错，而不是静默覆盖")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("错误信息应指出重复 tag：%v", err)
	}
}
