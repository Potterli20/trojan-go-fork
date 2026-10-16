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

// TestManyUDPSessionsDoNotStrandFDs 用一批"各说一句话就消失"的 UDP 会话压一遍中继链：
// forward 入站 → shadowsocks → mux → trojan → 服务端 → echo 目标。
//
// 单独测这个形态的理由：UDP 会话表是按**源地址**索引的，每个源端口一条会话，会话里
// 挂着通道、池化缓冲和回包 socket。只跑一条会话的用例数不出"每会话漏一点"，而 TCP
// 那侧由 abort_fd_test.go 覆盖。这里让每个会话都半途消失：不再发包、不等空闲超时，
// 直接把栈拆掉。
//
// 泄露判据复用 leakStacks（goroutineleak 的栈增量），fd 判据复用 openFDCount，
// 两者各自都已在别的用例里自证过敏感。
func TestManyUDPSessionsDoNotStrandFDs(t *testing.T) {
	serverPort := common.PickPort("tcp", "127.0.0.1")
	clientPort := common.PickPort("udp", "127.0.0.1")
	_, targetPort, _ := net.SplitHostPort(util.EchoAddr)

	clientData := fmt.Sprintf(`
run-type: forward
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: %d
target-addr: 127.0.0.1
target-port: %s
password:
    - password
ssl:
    verify: false
    fingerprint: firefox
    sni: localhost
shadowsocks:
    enabled: true
    method: AEAD_CHACHA20_POLY1305
    password: 12345678
mux:
    enabled: true
`, clientPort, serverPort, targetPort)
	serverData := fmt.Sprintf(`
run-type: server
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: 80
disable-http-check: true
password:
    - password
ssl:
    verify-hostname: false
    key: server.key
    cert: server.crt
    sni: localhost
shadowsocks:
    enabled: true
    method: AEAD_CHACHA20_POLY1305
    password: 12345678
`, serverPort)

	server, err := proxy.NewProxyFromConfigData([]byte(serverData), false)
	common.Must(err)
	go server.Run() //gosec:disable -- 错误忽略：测试用后台服务
	client, err := proxy.NewProxyFromConfigData([]byte(clientData), false)
	common.Must(err)
	go client.Run() //gosec:disable -- 错误忽略：测试用后台服务
	time.Sleep(2 * time.Second)

	defer func() {
		client.Close() //gosec:disable -- 错误忽略：测试清理
		server.Close() //gosec:disable -- 错误忽略：测试清理
	}()

	// 先跑几条会话并落定：把首次建监听、证书加载这类一次性 fd 排除在基线之外，
	// 同时确认链路真的通——一条都不通的话下面的比对毫无意义
	if echoed := runUDPSessions(t, clientPort, 3); echoed == 0 {
		t.Fatal("UDP 会话一条都没通：这条判据会是空转")
	}
	settle(t)
	fdBaseline := openFDCount(t)
	stackBaseline := totalLeaked(leakStacks(t))

	const sessions = 60
	ok := runUDPSessions(t, clientPort, sessions)
	if ok < sessions*4/5 {
		t.Fatalf("%d 个 UDP 会话只有 %d 个回声成功，链路不通，泄露比对没有意义", sessions, ok)
	}

	client.Close() //gosec:disable -- 错误忽略：关停路径，错误另有条目覆盖
	server.Close() //gosec:disable -- 错误忽略：关停路径，错误另有条目覆盖
	waitForLeakProfile(t)

	fdAfter := openFDCount(t)
	if grew := fdAfter - fdBaseline; grew > 6 {
		t.Fatalf("%d 个半途消失的 UDP 会话之后 fd 从 %d 涨到 %d（+%d）：有会话的 socket 或通道没回收",
			sessions, fdBaseline, fdAfter, grew)
	}
	stackAfter := totalLeaked(leakStacks(t))
	if grew := stackAfter - stackBaseline; grew > 0 {
		t.Fatalf("拆栈后本仓的泄露协程多了 %d 条（基线 %d，现在 %d）", grew, stackBaseline, stackAfter)
	}
	t.Logf("fd %d -> %d；泄露协程 %d -> %d；%d 个会话中 %d 个回声成功",
		fdBaseline, fdAfter, stackBaseline, stackAfter, sessions, ok)
}

// totalLeaked 把 leakStacks 的指纹表折成条数。
func totalLeaked(m map[string]int) int {
	n := 0
	for _, c := range m {
		n += c
	}
	return n
}

// runUDPSessions 开 n 个独立源端口的 UDP socket，各发一个包等回声，然后立刻关掉——
// 会话因此既没有正常结束路径，也没有等到空闲超时。返回回声成功的个数。
func runUDPSessions(t *testing.T, port int, n int) int {
	t.Helper()
	success := 0
	for i := range n {
		conn, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			continue
		}
		uc, ok := conn.(*net.UDPConn)
		if !ok {
			conn.Close() //gosec:disable -- 错误忽略：测试清理
			continue
		}
		payload := []byte(fmt.Sprintf("udp-session-%d", i))
		buf := make([]byte, 1024)
		// 环回 UDP 偶发丢包，重试到超时为止
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := uc.WriteTo(payload, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}); err != nil {
				break
			}
			if err := uc.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
				break
			}
			rn, _, err := uc.ReadFrom(buf)
			if err != nil {
				continue
			}
			if rn == len(payload) && string(buf[:rn]) == string(payload) {
				success++
			}
			break
		}
		uc.Close() //gosec:disable -- 错误忽略：会话半途消失
	}
	return success
}
