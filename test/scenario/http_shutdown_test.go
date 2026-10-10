package scenario

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/proxy"
	"github.com/Potterli20/trojan-go-fork/tunnel/trojan"
)

// origin 是一个可控体积的 HTTP 源站，并对外暴露"写到第几块/写完没有"的计数。
// 那个计数是本文件那条关停用例的前提自证：只有确认源站还写着写不完，才能证明
// 链路末端（入站 http handler 往本地 socket 写响应）真的堵住了，而不是
// "响应早就发完了、Close 当然快"这种永真判据。
type origin struct {
	written  atomic.Int64
	finished atomic.Bool
	srv      *http.Server
	ln       net.Listener
	port     string
}

func startOrigin(t *testing.T, totalChunks int) *origin {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听源站失败: %v", err)
	}
	chunk := strings.Repeat("A", 64*1024)
	o := &origin{ln: ln}
	o.srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		for range totalChunks {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
			o.written.Add(1)
			if fl != nil {
				fl.Flush()
			}
			time.Sleep(time.Millisecond) // 别把 CPU 写满，让流量控制真正生效
		}
		o.finished.Store(true)
	})}
	_, o.port, _ = net.SplitHostPort(ln.Addr().String())
	go o.srv.Serve(ln) //gosec:disable -- 错误忽略：测试夹具
	return o
}

func (o *origin) stop() {
	o.srv.Close() //gosec:disable -- 错误忽略：测试清理
	o.ln.Close()  //gosec:disable -- 错误忽略：测试清理
}

// startPair 起一对进程内的 server/client，返回客户端入站端口。
// 服务端用真实 TCP 连回本机的源站，所以整个转发链路都是真的。
func startPair(t *testing.T, o *origin) (client, server *proxy.Proxy, localPort int) {
	t.Helper()
	trojan.Auth = nil
	localPort = common.PickPort("tcp", "127.0.0.1")
	serverPort := common.PickPort("tcp", "127.0.0.1")
	clientData := fmt.Sprintf(`
run-type: client
local-addr: 127.0.0.1
local-port: %d
remote-addr: 127.0.0.1
remote-port: %d
password:
    - password
ssl:
    verify: false
    sni: localhost
`, localPort, serverPort)
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
`, serverPort, o.port)

	var err error
	server, err = proxy.NewProxyFromConfigData([]byte(serverData), false)
	if err != nil {
		t.Fatalf("创建服务端: %v", err)
	}
	go server.Run() //gosec:disable -- 错误忽略：测试夹具
	client, err = proxy.NewProxyFromConfigData([]byte(clientData), false)
	if err != nil {
		server.Close() //gosec:disable -- 错误忽略：测试清理
		t.Fatalf("创建客户端: %v", err)
	}
	go client.Run() //gosec:disable -- 错误忽略：测试夹具
	time.Sleep(2 * time.Second)
	return
}

// dialProxy 连上客户端的入站端口（HTTP 代理形式与 SOCKS 共用同一监听）。
func dialProxy(t *testing.T, localPort int) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), 5*time.Second)
	if err != nil {
		t.Fatalf("连接客户端入站失败: %v", err)
	}
	return conn
}

func proxyReq(conn net.Conn, o *origin, path string) error {
	req := fmt.Sprintf("GET http://127.0.0.1:%s%s HTTP/1.1\r\nHost: 127.0.0.1:%s\r\n\r\n", o.port, path, o.port)
	_, err := conn.Write([]byte(req))
	return err
}

// TestHTTPProxyServesKeepAliveOverRealStack 在真实栈上守住 keep-alive：
// 同一条入站连接依次两个请求，都必须拿到完整响应；随后关闭整个客户端，
// 并记录"没有在途响应"时的关停耗时作为对照。
//
// 这条在修复前后都必须为绿（修复前它就是绿的），否则它只是永真判据。
func TestHTTPProxyServesKeepAliveOverRealStack(t *testing.T) {
	o := startOrigin(t, 1) // 64KB，小到一个请求能完整读完
	defer o.stop()
	client, server, localPort := startPair(t, o)
	defer server.Close() //gosec:disable -- 错误忽略：测试清理

	conn := dialProxy(t, localPort)
	defer conn.Close() //gosec:disable -- 错误忽略：测试清理
	reader := bufio.NewReader(conn)

	for i, path := range []string{"/one", "/two"} {
		if err := proxyReq(conn, o, path); err != nil {
			t.Fatalf("第 %d 个请求写入失败: %v", i+1, err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		resp, err := http.ReadResponse(reader, &http.Request{Method: "GET", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1})
		if err != nil {
			t.Fatalf("第 %d 个请求读响应失败: %v（若连接被提前关掉，就是 keep-alive 退化成一个请求一条连接）", i+1, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("第 %d 个请求状态码 %d", i+1, resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close() //gosec:disable -- 错误忽略：测试清理
		if err != nil {
			t.Fatalf("第 %d 个请求读 body 失败: %v", i+1, err)
		}
		if len(body) != 64*1024 {
			t.Fatalf("第 %d 个请求 body 长度 %d，期望 65536", i+1, len(body))
		}
	}

	start := time.Now()
	client.Close() //gosec:disable -- 错误忽略：耗时才是本用例的重点
	elapsed := time.Since(start)
	t.Logf("对照：没有在途响应时的关停耗时 %v", elapsed)
	if elapsed > 3*time.Second {
		t.Fatalf("正常关停也要 %v：时限选得太松，那条在途响应用例会跟着失去意义", elapsed)
	}
}

// TestHTTPShutdownIsNotStalledByInflightResponse 盯真实栈上的关停延迟：
// 入站 handler 正把一个响应写回客户端、而客户端已经不再读取时，那一句 write 会一直
// 阻塞。修复前 http.Server.Close 里的 wg.Wait 没有上限，只能靠 Proxy.Close 的两段
// 5s 兜底退出——每次关停都白等，且裸连接一直被占着；修复后 s.ctx 一取消就关掉裸
// 连接，在途读写立刻报错返回。
func TestHTTPShutdownIsNotStalledByInflightResponse(t *testing.T) {
	o := startOrigin(t, 512) // 32MB，远超链路上所有缓冲之和
	defer o.stop()
	client, server, localPort := startPair(t, o)
	defer server.Close() //gosec:disable -- 错误忽略：测试清理

	conn := dialProxy(t, localPort)
	defer conn.Close() //gosec:disable -- 错误忽略：测试清理
	if err := proxyReq(conn, o, "/big"); err != nil {
		t.Fatalf("发送请求失败: %v", err)
	}

	// 只读走 1KB 就停手，且不关闭连接：让 handler 堵在写响应上
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	if _, err := io.ReadFull(conn, make([]byte, 1024)); err != nil {
		t.Fatalf("读取响应开头失败: %v", err)
	}

	// 前提自证：源站必须在限定时限内"还在写、但写不完"。
	// 写完了 = 客户端把 32MB 全读走了，本用例没有制造出在途写，判据就是空的。
	if !waitForClog(o, 10*time.Second) {
		t.Fatalf("没有制造出「响应写被堵住」的现场：源站已写完 %d 块（共 512），"+
			"说明客户端仍在读取，本用例没有覆盖在途响应", o.written.Load())
	}
	t.Logf("现场已就绪：源站写到第 %d 块仍未写完，链路末端被堵住", o.written.Load())

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		client.Close() //gosec:disable -- 错误忽略：耗时才是本用例的重点
		done <- time.Since(start)
	}()

	select {
	case elapsed := <-done:
		t.Logf("带在途响应时的关停耗时：%v", elapsed)
		if elapsed > 3*time.Second {
			t.Fatalf("关停耗时 %v：仍被一个在途 HTTP 响应拖住（修复前要靠两段 5s 兜底才能退出）", elapsed)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("关停 20s 仍未返回：入站 handler 卡在写响应上，无人唤醒")
	}
}

// waitForClog 确认"链尾堵死"这个前提真的成立了，分两步：
//  1. 源站必须先推进到至少 8 块——否则说明请求根本没通，现场也没造就出来；
//  2. 之后给 settle 时限：若这段时间结束时响应仍未发完，才证明下游确实有个
//     写被堵着（没堵住的话 512 块带 1ms 间隔远早于 10s 就写完了）。
//
// 注意第二步不能要求"仍在推进"：完全堵死时写入次数会冻住，那是正确状态。
func waitForClog(o *origin, settle time.Duration) bool {
	start := time.Now()
	for o.written.Load() < 8 {
		if o.finished.Load() || time.Since(start) > 15*time.Second {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
	deadline := time.Now().Add(settle)
	for time.Now().Before(deadline) {
		if o.finished.Load() {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
	return !o.finished.Load()
}
