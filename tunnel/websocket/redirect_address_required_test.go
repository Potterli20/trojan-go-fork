package websocket

import (
	"context"
	"strings"
	"testing"

	"github.com/Potterli20/trojan-go-fork/config"
)

// remote-addr 与 websocket.host 都为空时，诱饵地址会退化成 ":80"，而实测
// net.Dial("tcp", ":80") 连的是 127.0.0.1：未认证对端的字节被中继进代理自己主机的
// 本地服务。旧实现在这里只打两次 Warn 就继续，配置写错了永远没有症状。
func TestNewServerRejectsWhenNoDecoyAddressIsAvailable(t *testing.T) {
	cfg := &Config{RemoteHost: "", RemotePort: 0}
	cfg.Websocket.Host = ""
	_, err := NewServer(config.WithConfig(context.Background(), Name, cfg), nil)
	if err == nil {
		t.Fatal("两个兜底都为空时必须拒绝启动")
	}
	if !strings.Contains(err.Error(), "remote-addr") {
		t.Fatalf("报错要指出是诱饵地址缺失，实际：%v", err)
	}
}

// 正向对照：websocket.host 作为文档承诺的兜底仍然有效（AGENTS.md：它只在 remote-addr
// 留空时充当诱饵地址），这道闸不能把它一起拦掉。
func TestNewServerStillFallsBackToWebsocketHost(t *testing.T) {
	cfg := &Config{RemoteHost: "", RemotePort: 0}
	cfg.Websocket.Host = "decoy.example.org"
	if _, err := NewServer(config.WithConfig(context.Background(), Name, cfg), nil); err != nil &&
		strings.Contains(err.Error(), "both remote-addr and websocket.host are empty") {
		t.Fatalf("websocket.host 应当能兜底，却被空地址闸门拒掉：%v", err)
	}
}
