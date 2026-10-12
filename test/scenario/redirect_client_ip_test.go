package scenario

import (
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/proxy"
)

// decoy 是一个只记录收到的字节、不解释协议的诱饵/回落服务器。
type decoy struct {
	ln   net.Listener
	got  chan []byte
	port int
}

func startDecoy(t *testing.T) *decoy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	common.Must(err)
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	common.Must(err)
	d := &decoy{ln: ln, got: make(chan []byte, 8)}
	var p int
	fmt.Sscanf(portStr, "%d", &p)
	d.port = p
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close() //gosec:disable -- 错误忽略：诱饵夹具
				_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
				b, _ := io.ReadAll(c)
				d.got <- b
			}(c)
		}
	}()
	t.Cleanup(func() { ln.Close() }) //gosec:disable -- 错误忽略：诱饵夹具
	return d
}

// nextData 取下一条被中继进来的连接数据；连接可能因探测/重试而多条，
// 所以逐条读到出现请求行为止。
func (d *decoy) nextData(t *testing.T, want string, budget time.Duration) []byte {
	t.Helper()
	deadline := time.After(budget)
	for {
		select {
		case b := <-d.got:
			if want == "" || strings.Contains(string(b), want) {
				return b
			}
		case <-deadline:
			t.Fatalf("%v 内诱饵没有收到含 %q 的数据", budget, want)
		}
	}
}

func serverDataWithRedirect(localPort, decoyPort int, inject bool) string {
	yaml := fmt.Sprintf(`
run-type: server
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: %d
disable-http-check: true
password:
    - password
`, localPort, decoyPort)
	if inject {
		yaml += "redirect-client-ip: true\n"
	}
	yaml += fmt.Sprintf(`ssl:
    verify-hostname: false
    key: server.key
    cert: server.crt
    sni: localhost
    fallback-addr: 127.0.0.1
    fallback-port: %d
`, decoyPort)
	return yaml
}

// TestRedirectClientIPOverRealStack 是这次功能的最小端到端证据：走真实进程内代理 +
// 真实 TLS 入站 + 真实 redirector，配置从 YAML 文本一路解析进来。
//
// 现场是"明文打到 TLS 端口"：tls 握手报 first record does not look like a TLS
// handshake，连接被交给 fallback（即诱饵）。这正是诱饵路径在真实栈上的入口。
// 单元侧那些判据是直接构造 Config 结构体的，标签写错、注册漏掉都测不出来；
// 这条要的是"配置里的一个布尔值真的决定了发出去的字节"。
func TestRedirectClientIPOverRealStack(t *testing.T) {
	const req = "GET /probe HTTP/1.1\r\nHost: probe.example\r\n\r\n"

	run := func(t *testing.T, inject bool) []byte {
		t.Helper()
		d := startDecoy(t)
		localPort := common.PickPort("tcp", "127.0.0.1")
		p, err := proxy.NewProxyFromConfigData([]byte(serverDataWithRedirect(localPort, d.port, inject)), false)
		if err != nil {
			t.Fatalf("创建服务端: %v", err)
		}
		go p.Run()                      //gosec:disable -- 错误忽略：测试夹具
		t.Cleanup(func() { p.Close() }) //gosec:disable -- 错误忽略：测试清理
		time.Sleep(2 * time.Second)

		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), 5*time.Second)
		if err != nil {
			t.Fatalf("连接入站端口失败: %v", err)
		}
		defer conn.Close() //gosec:disable -- 错误忽略：测试清理
		if _, err := conn.Write([]byte(req)); err != nil {
			t.Fatalf("写入明文请求失败: %v", err)
		}
		// 关掉写向，让 redirector 把已缓冲的请求完整推给诱饵后收尾
		if tc, ok := conn.(*net.TCPConn); ok {
			tc.CloseWrite() //gosec:disable -- 错误忽略：测试夹具
		}
		return d.nextData(t, "GET /probe", 20*time.Second)
	}

	t.Run("开关打开", func(t *testing.T) {
		got := run(t, true)
		for _, want := range []string{"GET /probe HTTP/1.1", "X-Forwarded-For: 127.0.0.1", "X-Real-IP: 127.0.0.1"} {
			if !strings.Contains(string(got), want) {
				t.Fatalf("缺少 %q，诱饵实际收到：%q", want, string(got))
			}
		}
	})

	t.Run("默认关闭时逐字节原样", func(t *testing.T) {
		got := run(t, false)
		if string(got) != req {
			t.Fatalf("没开 redirect-client-ip 却改了字节：\n收到 %q\n发送 %q", string(got), req)
		}
	})
}
