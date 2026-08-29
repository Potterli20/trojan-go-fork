package scenario

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	netproxy "golang.org/x/net/proxy"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/proxy"
	"github.com/Potterli20/trojan-go-fork/test/util"
)

// socks5Upstream 是一个仅支持 TCP CONNECT（无认证）的最小 SOCKS5 服务，
// 用于在测试中模拟 freedom 的 forward_proxy 上游，并记录每一次 CONNECT 的目标地址。
type socks5Upstream struct {
	listener net.Listener

	mu      sync.Mutex
	targets []string

	wg sync.WaitGroup
}

func newSocks5Upstream(addr string) (*socks5Upstream, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &socks5Upstream{listener: listener}
	s.wg.Go(func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			s.wg.Go(func() {
				s.handleConn(conn)
			})
		}
	})
	return s, nil
}

func (s *socks5Upstream) handleConn(conn net.Conn) {
	defer conn.Close()
	greeting := make([]byte, 2)
	if _, err := io.ReadFull(conn, greeting); err != nil {
		return
	}
	methods := make([]byte, greeting[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	// 选择无认证
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return
	}
	if head[1] != 0x01 { // 仅支持 CONNECT
		conn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	var host string
	switch head[3] {
	case 0x01: // IPv4
		ip := make([]byte, 4)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	case 0x03: // 域名
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return
		}
		domain := make([]byte, length[0])
		if _, err := io.ReadFull(conn, domain); err != nil {
			return
		}
		host = string(domain)
	case 0x04: // IPv6
		ip := make([]byte, 16)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	default:
		return
	}
	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return
	}
	target := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(portBuf))))

	s.mu.Lock()
	s.targets = append(s.targets, target)
	s.mu.Unlock()

	upstream, err := net.Dial("tcp", target)
	if err != nil {
		conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) // 连接失败
		return
	}
	defer upstream.Close()
	if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	go io.Copy(upstream, conn)
	io.Copy(conn, upstream)
}

// targets 返回已记录的 CONNECT 目标副本
func (s *socks5Upstream) targetsSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.targets...)
}

// TestServerForwardProxyRouting 验证服务端对 forward_proxy 的分流（issue #98）：
// 服务端配置 freedom 的 forward_proxy（上游 SOCKS5）与 router 后，
// 命中 proxy 规则的流量应经上游转发，命中 bypass 规则（含默认策略）的流量应直连目标。
func TestServerForwardProxyRouting(t *testing.T) {
	serverPort := common.PickPort("tcp", "127.0.0.1")
	socksPort := common.PickPort("tcp", "127.0.0.1")
	clientPort := common.PickPort("tcp", "127.0.0.1")
	bypassPort := common.PickPort("tcp", "127.0.0.2")

	upstream, err := newSocks5Upstream(fmt.Sprintf("127.0.0.1:%d", socksPort))
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.listener.Close()

	// 直连目标（bypass 分流终点），绑定在 127.0.0.2 上与回显目标（127.0.0.1）区分
	bypassListener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.2:%d", bypassPort))
	if err != nil {
		t.Fatal(err)
	}
	defer bypassListener.Close()
	bypassDone := make(chan struct{})
	go func() {
		defer close(bypassDone)
		for {
			conn, err := bypassListener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				io.Copy(conn, conn)
			}()
		}
	}()

	_, echoPortStr, _ := net.SplitHostPort(util.EchoAddr)

	serverData := fmt.Sprintf(`
run-type: server
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: %s
disable-http-check: true
password:
    - password
tcp:
    fast_open: false
transport-plugin:
    enabled: true
    type: plaintext
forward-proxy:
    enabled: true
    proxy-addr: 127.0.0.1
    proxy-port: %d
router:
    enabled: true
    domain-strategy: as_is
    default-policy: bypass
    proxy:
        - "cidr:127.0.0.1/32"
`, serverPort, echoPortStr, socksPort)

	clientData := fmt.Sprintf(`
run-type: client
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: %d
password:
    - password
tcp:
    fast_open: false
transport-plugin:
    enabled: true
    type: plaintext
`, clientPort, serverPort)

	server, err := proxy.NewProxyFromConfigData([]byte(serverData), false)
	if err != nil {
		t.Fatal(err)
	}
	go server.Run()
	client, err := proxy.NewProxyFromConfigData([]byte(clientData), false)
	if err != nil {
		t.Fatal(err)
	}
	go client.Run()
	defer func() {
		client.Close()
		server.Close()
	}()

	time.Sleep(time.Second * 2)

	// 探针：确认服务端 trojan 端口可达
	probe, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", serverPort), time.Second)
	if err != nil {
		t.Fatal("server port not reachable:", err)
	}
	probe.Close()

	dialer, err := netproxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", clientPort), nil, netproxy.Direct)
	if err != nil {
		t.Fatal(err)
	}

	// 场景一：命中 proxy 规则（127.0.0.1/32）→ 流量应经 forward_proxy 上游转发到回显目标
	proxyConn, err := dialer.Dial("tcp", util.EchoAddr)
	if err != nil {
		t.Fatal("proxy policy dial failed:", err)
	}
	payload := util.GeneratePayload(1024)
	if _, err := proxyConn.Write(payload); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024)
	if _, err := io.ReadFull(proxyConn, buf); err != nil {
		t.Fatal(err)
	}
	proxyConn.Close()
	if !bytes.Equal(payload, buf) {
		t.Fatal("proxy policy relay payload mismatch")
	}

	// 场景二：默认策略 bypass（127.0.0.2）→ 流量应直连目标，不经过 forward_proxy 上游
	bypassConn, err := dialer.Dial("tcp", fmt.Sprintf("127.0.0.2:%d", bypassPort))
	if err != nil {
		t.Fatal("bypass policy dial failed:", err)
	}
	payload2 := util.GeneratePayload(1024)
	if _, err := bypassConn.Write(payload2); err != nil {
		t.Fatal(err)
	}
	buf2 := make([]byte, 1024)
	if _, err := io.ReadFull(bypassConn, buf2); err != nil {
		t.Fatal(err)
	}
	bypassConn.Close()
	if !bytes.Equal(payload2, buf2) {
		t.Fatal("bypass policy relay payload mismatch")
	}

	// 分流验证：上游只应记录 proxy 规则目标的 CONNECT，不应出现 bypass 目标
	time.Sleep(time.Millisecond * 500)
	recorded := upstream.targetsSnapshot()
	if len(recorded) != 1 || recorded[0] != util.EchoAddr {
		t.Fatalf("unexpected forward_proxy upstream targets: %v, want only %s", recorded, util.EchoAddr)
	}
}
