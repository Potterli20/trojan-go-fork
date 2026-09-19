package scenario

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/proxy"
	"github.com/Potterli20/trojan-go-fork/test/util"
)

// TestCleanShutdownHasNoError 校验正常停机的端到端契约：
// Run/Close 干净走完时不得返回错误（main 只在 Handle 返回 nil 时以退出码 0 结束），
// 且监听端口必须真的被释放。
func TestCleanShutdownHasNoError(t *testing.T) {
	serverPort := common.PickPort("tcp", "127.0.0.1")
	serverData := fmt.Sprintf(`
run-type: server
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: %s
disable-http-check: true
password:
    - password
ssl:
    verify-hostname: false
    key: server.key
    cert: server.crt
    sni: localhost
`, serverPort, util.HTTPPort)

	server, err := proxy.NewProxyFromConfigData([]byte(serverData), false)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Run() }()

	// 等服务端真正开始监听
	if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", serverPort), 5*time.Second); err != nil {
		t.Fatal("server port not reachable:", err)
	} else {
		c.Close()
	}

	if err := server.Close(); err != nil {
		t.Fatalf("clean shutdown must not report an error: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Close")
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", serverPort))
	if err != nil {
		t.Fatalf("port %d is still occupied after Close: %v", serverPort, err)
	}
	ln.Close()

	// 重复 Close 也必须静默成功：服务端栈里多个端点会共用同一批底层监听器，
	// 第二个端点关到已经关掉的监听器不属于故障。
	if err := server.Close(); err != nil {
		t.Fatalf("repeated Close must not report an error: %v", err)
	}
}
