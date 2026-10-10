package proxy_test

import (
	"fmt"
	"net"
	"testing"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/proxy"

	// 注册 SERVER / CLIENT 两种 run_type 的 creator 与其配置
	_ "github.com/Potterli20/trojan-go-fork/proxy/client"
	_ "github.com/Potterli20/trojan-go-fork/proxy/server"
)

// 这两条测试盯的是同一个不变量：**构建失败必须回收已经建好的 tunnel**。
// 入站 tunnel 会真的绑定监听端口，而 context 的 cancel() 不关闭任何东西；修复前
// proxy/client 与 proxy/server 的每条提前返回都只 cancel()，于是一次配置写错就永久
// 留下 TCP+UDP 监听与后台 goroutine（同进程内重建还会直接 bind 失败）。
// 判据用最直观的 observable：失败返回之后，那个端口必须能被重新绑定。

func TestFailedServerCreationReleasesListeners(t *testing.T) {
	port := common.PickPort("udp", "127.0.0.1")
	// transport 先绑定监听，随后 tls 因证书路径不存在而失败
	cfg := fmt.Sprintf(`{
	  "run_type": "server",
	  "local_addr": "127.0.0.1",
	  "local_port": %d,
	  "password": ["deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"],
	  "ssl": { "cert": "/nonexistent/trojan.crt", "key": "/nonexistent/trojan.key" },
	  "log_level": 3
	}`, port)

	if _, err := proxy.NewProxyFromConfigData([]byte(cfg), true); err == nil {
		t.Fatal("预期因证书路径错误而构建失败，却成功了——测试无法再复现该泄露")
	}
	assertPortReleased(t, port)
}

func TestFailedClientCreationReleasesListeners(t *testing.T) {
	port := common.PickPort("udp", "127.0.0.1")
	// 入站 adapter（TCP+UDP）先建好，之后 CreateClientStack 因无效算法失败
	cfg := fmt.Sprintf(`{
	  "run_type": "client",
	  "local_addr": "127.0.0.1",
	  "local_port": %d,
	  "remote_addr": "127.0.0.1",
	  "remote_port": 443,
	  "password": ["deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"],
	  "shadowsocks": { "enabled": true, "method": "bogus-cipher", "password": "x" },
	  "log_level": 3
	}`, port)

	if _, err := proxy.NewProxyFromConfigData([]byte(cfg), true); err == nil {
		t.Fatal("预期因无效 shadowsocks 算法而构建失败，却成功了——测试无法再复现该泄露")
	}
	assertPortReleased(t, port)
}

func assertPortReleased(t *testing.T, port int) {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	tcpLn, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("构建失败后 TCP 端口 %d 仍被占用，说明入站监听未回收: %v", port, err)
	}
	tcpLn.Close()

	udpLn, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatalf("构建失败后 UDP 端口 %d 仍被占用，说明入站监听未回收: %v", port, err)
	}
	udpLn.Close()
}
