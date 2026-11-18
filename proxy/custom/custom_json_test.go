package custom

import (
	"context"
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/tunnel/socks"
)

// TestBuildNodesFromJSONConfigKeepsNodeParams 钉住 custom 模式的 JSON 配置：
// 顶层 .json 配置文件经 WithJSONConfig 解析后，NodeConfig.Config 是下划线键的
// map[string]any（如 local_addr）。buildNodes 原先直接 yaml.Marshal 这个 map 再
// WithYAMLConfig——而各模块配置用的是 hyphen 的 yaml 标签（local-addr），
// 于是 JSON 配置下 custom 节点参数被静默丢弃（socks 监听在 :0、UDP timeout 默认）。
// 这是"接受但不生效"缺陷：进程照常启动，用户以为配好了实际没有。
//
// 变异对照：把 buildNodes 里的 normalize 去掉，本用例转红。
func TestBuildNodesFromJSONConfigKeepsNodeParams(t *testing.T) {
	// 模拟 JSON 解析 node.config 得到的结果：下划线键的 map。
	var cfg NodeConfig
	if err := json.Unmarshal([]byte(`{
		"protocol":"socks","tag":"socks-in",
		"config":{"local_addr":"1.2.3.4","local_port":9999,"udp_timeout":42}
	}`), &cfg); err != nil {
		t.Fatal(err)
	}

	nodes, err := buildNodes(context.Background(), []NodeConfig{cfg}, true)
	if err != nil {
		t.Fatalf("buildNodes 失败：%v", err)
	}
	node := nodes["socks-in"]
	if node == nil {
		t.Fatal("节点未生成")
	}
	got, ok := config.FromContext(node.Context, socks.Name).(*socks.Config)
	if !ok {
		t.Fatalf("节点上下文里没有 socks.Config：%T", config.FromContext(node.Context, socks.Name))
	}
	if got.LocalHost != "1.2.3.4" || got.LocalPort != 9999 || got.UDPTimeout != 42 {
		t.Fatalf("JSON 配置节点参数丢失：LocalHost=%q LocalPort=%d UDPTimeout=%d",
			got.LocalHost, got.LocalPort, got.UDPTimeout)
	}
}

// TestBuildNodesFromYAMLConfigStillWorks 是 YAML 来源的正向对照：修 JSON 分流后，
// 原本正常的 YAML（hyphen 键）行为不能退化。YAML 解析出的 map 键是 local-addr 风格，
// 必须继续走 YAML 解析器。
func TestBuildNodesFromYAMLConfigStillWorks(t *testing.T) {
	var cfg NodeConfig
	if err := yaml.Unmarshal([]byte(`
protocol: socks
tag: socks-in
config:
  local-addr: 5.6.7.8
  local-port: 8888
  udp-timeout: 17
`), &cfg); err != nil {
		t.Fatal(err)
	}
	nodes, err := buildNodes(context.Background(), []NodeConfig{cfg}, false)
	if err != nil {
		t.Fatalf("buildNodes 失败：%v", err)
	}
	node := nodes["socks-in"]
	if node == nil {
		t.Fatal("节点未生成")
	}
	got, ok := config.FromContext(node.Context, socks.Name).(*socks.Config)
	if !ok {
		t.Fatalf("节点上下文里没有 socks.Config：%T", config.FromContext(node.Context, socks.Name))
	}
	if got.LocalHost != "5.6.7.8" || got.LocalPort != 8888 || got.UDPTimeout != 17 {
		t.Fatalf("YAML 配置节点参数丢失：LocalHost=%q LocalPort=%d UDPTimeout=%d",
			got.LocalHost, got.LocalPort, got.UDPTimeout)
	}
}
