package adapter

import (
	"context"
	"testing"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// TestAcceptPacketCloseDoesNotStealTheSharedListener 盯的是所有权：
// AcceptPacket 交出的句柄内嵌的是 Server 自己的 UDP 监听。上层（proxy 的包转发环）
// 会在会话结束时 Close 它，那等于替 Server 把监听关了；随后真正的 Server.Close
// 第一次关就拿到 "use of closed network connection"，client 模式带 UDP 的正常关停
// 会以 FATAL + 非零退出码收场。
func TestAcceptPacketCloseDoesNotStealTheSharedListener(t *testing.T) {
	port := common.PickPort("udp", "127.0.0.1")
	ctx := config.WithConfig(context.Background(), Name, &Config{
		LocalHost: "127.0.0.1",
		LocalPort: port,
	})
	server, err := NewServer(ctx, nil)
	common.Must(err)

	pc, err := server.AcceptPacket(&Tunnel{})
	if err != nil {
		t.Fatalf("AcceptPacket: %v", err)
	}
	var _ tunnel.PacketConn = pc

	if err := pc.Close(); err != nil { //gosec:disable -- 错误检查：被测行为
		t.Fatalf("会话句柄 Close 返回错误: %v", err)
	}

	// 归还会话之后，Server 自己必须仍能干净关闭
	if err := server.Close(); err != nil { //gosec:disable -- 错误检查：被测行为
		t.Fatalf("上层关闭会话后 Server.Close 报错：%v "+
			"——交出的句柄不该拥有共享监听的所有权", err)
	}
}
