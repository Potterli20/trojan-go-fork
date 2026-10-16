package quic

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	tlstunnel "github.com/Potterli20/trojan-go-fork/tunnel/tls"
)

// TestServerRejectsHostnameAsListenAddress 盯服务端监听地址的静默扩面。
//
// 服务端把 remote-addr 当作**要绑定的地址**（README 与 docs/quic_usage.md 都这么写），
// 而旧实现是 net.ParseIP(cfg.RemoteHost) 直接塞进 UDPAddr：域名解析成 nil，
// &net.UDPAddr{IP: nil} 的含义却是"监听所有网卡"。客户端那一侧 remote-addr 本来就是
// 域名，所以照抄/改一份配置就会不声不响地把服务从"只绑这个地址"变成"全接口可达"——
// 绑定成功、无告警、无报错，运维从日志里看不出来。
func TestServerRejectsHostnameAsListenAddress(t *testing.T) {
	certPath, keyPath := writeSelfSigned(t, t.TempDir())
	port := common.PickPort("udp", "127.0.0.1")

	ctx := config.WithConfig(context.Background(), Name, &Config{
		RemoteHost: "quic.example.test", // 域名：不是可绑定的地址
		RemotePort: port,
		QUIC:       QUICConfig{ALPN: "hq-29", MaxIdleTimeout: 30, MaxIncomingStreams: 100},
	})
	ctx = config.WithConfig(ctx, tlstunnel.Name, &tlstunnel.Config{
		TLS: tlstunnel.TLSConfig{CertPath: certPath, KeyPath: keyPath},
	})

	srv, err := NewServer(ctx, nil)
	if err == nil {
		srv.Close() //gosec:disable -- 错误忽略：测试清理
		t.Fatal("remote-addr 写域名时静默启动了：实际监听的是所有网卡，而配置想绑的是这一个地址")
	}
	if !strings.Contains(err.Error(), "not an IP literal") {
		t.Fatalf("拒绝原因不对：%v", err)
	}

	// 留空早在 :243 就有独立校验（"listen address is empty"），不能被这条新判空
	// 改变语义——它本来就不是"全部接口"的意思，想全绑要显式写 0.0.0.0
	emptyHost := config.WithConfig(context.Background(), Name, &Config{
		RemoteHost: "",
		RemotePort: common.PickPort("udp", "127.0.0.1"),
		QUIC:       QUICConfig{ALPN: "hq-29", MaxIdleTimeout: 30, MaxIncomingStreams: 100},
	})
	emptyHost = config.WithConfig(emptyHost, tlstunnel.Name, &tlstunnel.Config{
		TLS: tlstunnel.TLSConfig{CertPath: certPath, KeyPath: keyPath},
	})
	if _, err := NewServer(emptyHost, nil); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("留空应当继续被原有的「地址为空」校验拒绝，实际：%v", err)
	}

	// 显式写 IP 必须照旧可用
	s3, err := NewServer(quicServerCtx(t, common.PickPort("udp", "127.0.0.1"), certPath, keyPath), nil)
	if err != nil {
		t.Fatalf("写 127.0.0.1 时被拒了：%v", err)
	}
	defer s3.Close() //gosec:disable -- 错误忽略：测试清理
	if _, ok := s3.listener.Addr().(*net.UDPAddr); !ok {
		t.Fatal("监听地址类型不对")
	}
}
