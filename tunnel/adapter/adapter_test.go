package adapter

import (
	"context"
	"testing"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
)

// TestServerCloseIsIdempotent 复刻 client 模式的关停路径：socks 与 http 两个端点
// 共用同一个 adapter 实例（proxy/client/client.go:61），而 proxy.releaseTunnels
// 逐个关 source，于是 adapter 的 Close 必然被调用两次。修复前第二次会真的去关
// udpListener，拿到 "use of closed network connection" 并原样返回，一路冒到
// main.go 变成「Invalid options」+ 非零退出码——正常 SIGTERM 关停也被判成失败。
func TestServerCloseIsIdempotent(t *testing.T) {
	var server *Server
	// 端口是 tcp/udp 同值复用，PickPort 只看 tcp，撞车就重试几次
	for range 5 {
		port := common.PickPort("tcp", "127.0.0.1")
		ctx := config.WithConfig(context.Background(), Name, &Config{
			LocalHost: "127.0.0.1",
			LocalPort: port,
		})
		s, err := NewServer(ctx, nil)
		if err == nil {
			server = s
			break
		}
	}
	if server == nil {
		t.Fatal("多次尝试仍无法在 127.0.0.1 上建立 adapter 监听")
	}

	if err := server.Close(); err != nil {
		t.Fatalf("第一次 Close 失败: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("第二次 Close 返回错误: %v", err)
	}
}
