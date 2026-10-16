package scenario

import (
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/proxy"
)

// openFDCount 数一遍本进程打开的 fd。代理最怕的不是堆内存而是 fd：
// 一条被弃用的连接在堆上可能只有几百字节，但端口/文件句柄是有硬上限的。
func openFDCount(t *testing.T) int {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("需要 /proc/self/fd")
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("无法读取 /proc/self/fd，跳过 fd 计数：%v", err)
	}
	return len(entries)
}

// TestAbortedPeersDoNotStrandFDs 用三种"半途而废"的对端形态轰炸服务栈，
// 然后要求 fd 回到基线：
//
//	a) 连上就关，一个字节都不发
//	b) 只发几字节就断（TLS 握手停在半路）
//	c) 走完 TLS 握手，再交一个错密码（认证失败 → 关闭/重定向这一支）
//
// 这三条都不走"正常会话被 relay 关掉"的路径，恰恰是 Close/回收逻辑最容易漏的出口：
// 握手中途失败的裸连接、认证失败后的连接、以及从未被上层取走的队列项。
// 正常流量的泄露由 goroutineleak_test.go 的栈增量判据负责，两者互补。
func TestAbortedPeersDoNotStrandFDs(t *testing.T) {
	serverPort := common.PickPort("tcp", "127.0.0.1")
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
`, serverPort)

	server, err := proxy.NewProxyFromConfigData([]byte(serverData), false)
	common.Must(err)
	go server.Run() //gosec:disable -- 错误忽略：测试用后台服务
	time.Sleep(2 * time.Second)

	addr := fmt.Sprintf("127.0.0.1:%d", serverPort)
	const rounds = 40

	// 先跑一轮并等它落定，把"服务端首次建监听/首次加载证书"这类一次性 fd 排除在基线外
	warm := abortAll(t, addr, 4)
	if warm.hello == 0 {
		t.Fatal("一轮都没连上：服务端没起来或地址写错，这条判据会是空转")
	}
	settle(t)
	baseline := openFDCount(t)

	// 自证：握着 8 条连接不放，fd 必须涨得起来。数不出"人为造的泄露"的计数，
	// 报"没有泄露"就只是计数不敏感而已。
	grow := make([]net.Conn, 0, 8)
	for len(grow) < 8 {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			break
		}
		grow = append(grow, c)
	}
	if len(grow) < 8 {
		t.Fatalf("只握住 %d 条连接，构造不出 fd 增长", len(grow))
	}
	runtime.GC()
	time.Sleep(300 * time.Millisecond)
	probe := openFDCount(t) - baseline
	for _, c := range grow {
		c.Close() //gosec:disable -- 错误忽略：测试清理
	}
	if probe < 8 {
		t.Fatalf("握着 8 条连接 fd 只涨了 %d：/proc/self/fd 计数不敏感，判据不可信", probe)
	}
	settle(t)
	if back := openFDCount(t) - baseline; back > 2 {
		t.Fatalf("松开握住的连接后 fd 没有回落（+%d），计数口径有问题", back)
	}

	stats := abortAll(t, addr, rounds)
	settle(t)

	after := openFDCount(t)
	if grew := after - baseline; grew > 6 {
		t.Fatalf("%d 组半途而废的对端（连上就关 %d / 停在半路 %d / 错密码 %d）之后 fd 从 %d 涨到 %d（+%d）：有连接没有被回收",
			rounds, stats.hello, stats.partial, stats.badAuth, baseline, after, grew)
	}
	if stats.badAuth == 0 {
		t.Logf("注意：TLS 握手成功+错密码这一支一次都没跑到（hello=%d partial=%d），该路径未被覆盖",
			stats.hello, stats.partial)
	}
	t.Logf("fd 基线 %d，风暴后 %d（连上就关 %d / 停在半路 %d / 错密码 %d）",
		baseline, after, stats.hello, stats.partial, stats.badAuth)

	common.Must(server.Close())
}

// abortStats 记录三种形态各自实际拨成功了几次。
type abortStats struct {
	hello   int // 连上就关
	partial int // TLS 握手停在半路
	badAuth int // 握手成功但密码错
}

// abortAll 跑一轮三种中止形态，并回报每种形态跑到了几次。
func abortAll(t *testing.T, addr string, n int) abortStats {
	t.Helper()
	var s abortStats
	for range n {
		// a) 连上就关
		c, err := net.Dial("tcp", addr)
		if err == nil {
			s.hello++
			c.Close() //gosec:disable -- 错误忽略：测试刻意中止
		}

		// b) TLS 握手停在半路
		c2, err := net.Dial("tcp", addr)
		if err == nil {
			s.partial++
			_, _ = c2.Write([]byte{0x16, 0x03, 0x01, 0x00, 0x05, 0x01, 0x00})
			c2.Close() //gosec:disable -- 错误忽略：测试刻意中止
		}

		// c) 握手成功 + 错密码
		tc, err := tlsDial(addr)
		if err == nil {
			s.badAuth++
			_, _ = tc.Write([]byte("00000000000000000000000000000000\r\n"))
			tc.Close() //gosec:disable -- 错误忽略：测试刻意中止
		}
	}
	return s
}

func tlsDial(addr string) (*tls.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		conn.Close() //gosec:disable -- 错误忽略：测试清理
		return nil, err
	}
	tconn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, ServerName: host})
	if err := tconn.Handshake(); err != nil {
		tconn.Close() //gosec:disable -- 错误忽略：测试清理
		return nil, err
	}
	return tconn, nil
}

// settle 给拆除留出有界时间：轮询到 fd 数连续两次不再变化为止。
func settle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	prev, stable := -1, 0
	for time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(200 * time.Millisecond)
		cur := openFDCount(t)
		if cur == prev {
			if stable++; stable >= 2 {
				return
			}
		} else {
			stable, prev = 0, cur
		}
	}
}
