package http

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/http2"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// listenServer 用真实 TCP 监听当 underlay：HTTP/2 需要一条能持续双向读写的流，
// head_bound_test.go 里那个"只交出一条预置连接"的 pipeServer 不够用。
type listenServer struct {
	ln net.Listener
}

func (l *listenServer) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	c, err := l.ln.Accept()
	if err != nil {
		return nil, err
	}
	return &pipeConn{Conn: c}, nil
}

func (l *listenServer) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, common.NewError("no packet")
}

func (l *listenServer) Close() error { return l.ln.Close() }

// startH2Proxy 起一个 http 入站 + 一个假 relay：假 relay 把请求读走、回一个固定响应，
// 每收到一条会话就计一次数，用来证明"h2 的每条流都变成一个转发会话"。
// 同时把 *Server 交回调用方，关停时长的判据要自己驱动 Close。
func startH2Proxy(t *testing.T) (client *http.Client, srv *Server, sessions *sync.Map) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	srv, err = NewServer(context.Background(), &listenServer{ln: ln})
	if err != nil {
		ln.Close() //gosec:disable -- 错误忽略：测试清理
		t.Fatalf("NewServer: %v", err)
	}

	sessions = &sync.Map{}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			conn, err := srv.AcceptConn(&Tunnel{})
			if err != nil {
				return
			}
			sessions.Store(conn.Metadata().Address.String(), struct{}{})
			wg.Go(func() {
				defer conn.Close()
				// 假 relay 里不做断言也不写日志：这些 goroutine 可能在测试
				// 结束后才退出，届时 t.* 会直接 panic。
				if err := readHeaders(conn); err != nil {
					return
				}
				const resp = "HTTP/1.1 200 OK\r\nContent-Length: 5\r\nX-Trojan-Test: h2\r\n\r\nhello"
				conn.Write([]byte(resp)) //gosec:disable -- 错误忽略：假 relay，写失败由客户端侧断言兜住
			})
		}
	})
	// 顺序要紧：srv.Close() 会取消 ctx 并关掉底层监听，AcceptConn 因此返回错误、
	// accept goroutine 才会退出；先 wg.Wait() 就是自己锁死。
	// 已经在用例里关过一次的，这里再关是幂等的（cancel/已关 listener/Wait 都可重复）。
	// 关停放后台：一旦关停唤醒被改坏，本文件那条 3s 判据会先报错，而不是让整个包
	// 挂到包级 -timeout 才失败（与 shutdown_wake_test.go 同一取舍）。
	t.Cleanup(func() {
		go func() {
			srv.Close() //gosec:disable -- 错误忽略：测试清理
			wg.Wait()
		}()
	})

	h2tr := &http2.Transport{
		AllowHTTP: true, // h2c：明文直连，靠先前知识
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, ln.Addr().String()) // 一律连到代理入口
		},
	}
	return &http.Client{Transport: h2tr, Timeout: 20 * time.Second}, srv, sessions
}

// TestH2CProxyRequestIsRelayed 是本特性的主判据：一条 h2c 连接上的绝对目标请求
// 必须变成一次转发会话，目标取自 :authority，响应要原样回到 h2 客户端。
//
// 为什么必须断言 resp.Proto == 2：不这样的话，只要嗅探没生效、这条连接被当成 h1
// 处理，客户端可能会拿到一个解析错误的响应而测试照样"通了"——判据就成了空转。
func TestH2CProxyRequestIsRelayed(t *testing.T) {
	client, _, sessions := startH2Proxy(t)

	req, err := http.NewRequest(http.MethodGet, "http://target.example.org/index.html", nil)
	if err != nil {
		t.Fatalf("构造请求: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("h2c 请求失败: %v", err)
	}
	defer resp.Body.Close() //gosec:disable -- 错误忽略：测试清理

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读响应体失败: %v", err)
	}
	if resp.ProtoMajor != 2 {
		t.Fatalf("响应不是走 HTTP/2 拿到的（Proto=%s）：前文嗅探没生效", resp.Proto)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "hello" {
		t.Fatalf("响应不对：status=%d body=%q", resp.StatusCode, body)
	}
	if got := resp.Header.Get("X-Trojan-Test"); got != "h2" {
		t.Fatalf("目标端的响应头没带回来：%q", got)
	}

	// 目的地必须来自 :authority，而不是某个默认值
	found := false
	sessions.Range(func(k, _ any) bool {
		if strings.Contains(k.(string), "target.example.org") {
			found = true
		}
		return true
	})
	if !found {
		t.Fatal(":authority 没有被解析成转发目的地")
	}
}

// TestH2CConcurrentStreamsBecomeSeparateSessions 要的是"多路复用"这件事本身：
// 同一条 TCP 连接上并发三条流，必须变成三个独立的转发会话。
// 只验单流的话，实现哪怕退化成"一条连接只服务一个请求"也是绿的。
func TestH2CConcurrentStreamsBecomeSeparateSessions(t *testing.T) {
	client, _, sessions := startH2Proxy(t)

	const n = 3
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Go(func() {
			url := fmt.Sprintf("http://host%d.example.org/", i)
			resp, err := client.Get(url)
			if err != nil {
				errs <- err
				return
			}
			defer resp.Body.Close() //gosec:disable -- 错误忽略：测试清理
			if _, err := io.Copy(io.Discard, resp.Body); err != nil {
				errs <- err
				return
			}
			errs <- nil
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("并发流请求失败: %v", err)
		}
	}

	hosts := map[string]bool{}
	sessions.Range(func(k, _ any) bool {
		hosts[k.(string)] = true
		return true
	})
	if len(hosts) < n {
		t.Fatalf("只派发出 %d 个转发会话，期望 %d 个（并发流被串行了）：%v", len(hosts), n, hosts)
	}
}

// TestH2CServerCloseWakesIdleConn 钉的是 h2 分支的关停唤醒。
//
// 这条判据不空转，靠两层证据：
//  1. 时限。ServeConn 的契约是"阻塞到连接不可读"，而这一版 http2 的 serverConn
//     里没有任何 ctx.Done() 监听（全文件零命中），opts.Context 也只影响各请求自己
//     的 ctx。所以 Close 能在 3s 内返回，只有"裸连接被关掉"这一条路。
//  2. 反证。Close 返回后再发一个请求必须"很快地"失败。若实现只是提前返回而没真关
//     连接，那条空闲的 h2 连接还活着 —— 少了这一层，"把 Close 里的 wg.Wait 删掉"
//     这种假修复也是绿的（实测确实在这个变异上绿过一次，详见下面注释）。
func TestH2CServerCloseWakesIdleConn(t *testing.T) {
	client, srv, _ := startH2Proxy(t)

	resp, err := client.Get("http://idle.example.org/")
	if err != nil {
		t.Fatalf("首个 h2c 请求失败: %v", err)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		resp.Body.Close() //gosec:disable -- 错误忽略：测试清理
		t.Fatalf("读干首个响应失败: %v", err)
	}
	resp.Body.Close() //gosec:disable -- 错误忽略：测试清理
	// 走到这里连接是空闲的：http2.Transport 会把它留在连接池里复用。

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		_ = srv.Close() //gosec:disable -- 错误忽略：本用例就是来测它
		done <- time.Since(start)
	}()

	var elapsed time.Duration
	select {
	case elapsed = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close 超过 3s 仍未返回：空闲的 h2 连接把关停拖死了。" +
			"ServeConn 不会自己观察 s.ctx，关停必须关掉裸连接才能收掉 handler")
	}

	// 反证：连接确实死了。这里复用同一个 client，才能打到连接池里那条空闲连接
	// （换一个新 Transport 只会去拨一个新 TCP，监听已关必然失败，什么也证明不了）。
	//
	// 只断言"报错"是不够的：实测「假修复＝把 Close 里的 wg.Wait 删掉」也是红的
	// ——那时裸连接还活着，但下游 relay 已经不再消费会话，请求会一直挂到客户端
	// 超时才失败。所以额外要求失败得很快：连接被真正关闭时，h2 客户端立刻收到
	// 复位/EOF，在回环上量级是毫秒。
	client.Timeout = 3 * time.Second
	reqStart := time.Now()
	resp2, err := client.Get("http://idle.example.org/")
	if err == nil {
		resp2.Body.Close() //gosec:disable -- 错误忽略：测试清理
		t.Fatalf("Close 只用了 %v 就返回，但客户端仍能复用这条连接拿到响应——"+
			"裸连接并没被关掉，handler 其实还活着", elapsed)
	}
	if took := time.Since(reqStart); took > time.Second {
		t.Fatalf("Close 返回后的请求是挂了 %v 才失败的（不是立刻断开）："+
			"裸连接没被关闭，只是再没人消费会话了。错误：%v", took, err)
	}
	t.Logf("空闲 h2 连接的关停耗时：%v；关停后请求在 %v 内失败", elapsed, time.Since(reqStart))
}

// TestH2CServerCloseWakesInflightStream 盯的是另一条没人跑过的路径：关停发生时
// 流还在途（请求已经转发走、响应还没回来）。
//
// 这里的假 relay 刻意照真实 relay 的样子做：proxy/proxy.go 的会话协程带的是
// defer inbound.Close()，所以关停时它会主动把会话关掉。本用例要证明的是 h2 这一侧
// 能接住这件事——ServeHTTP 停在 ReadResponse 上等 respReader，只有会话被关掉才能
// 返回；handler 不返回，ServeConn 就退出不了，Server.Close 的 wg.Wait 也收不了尾。
// 换句话说，这条钉的是「h2 的每个出口都关掉了本条流的会话」（含失败出口）。
func TestH2CServerCloseWakesInflightStream(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	srv, err := NewServer(context.Background(), &listenServer{ln: ln})
	if err != nil {
		ln.Close() //gosec:disable -- 错误忽略：测试清理
		t.Fatalf("NewServer: %v", err)
	}

	inflight := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			conn, err := srv.AcceptConn(&Tunnel{})
			if err != nil {
				return
			}
			wg.Go(func() {
				defer conn.Close()
				if err := readHeaders(conn); err != nil {
					return
				}
				// 不回响应：让 handler 停在读响应上，模拟"目标端还没答完"
				once.Do(func() { close(inflight) })
				<-srv.ctx.Done()
			})
		}
	})
	t.Cleanup(func() {
		go func() {
			srv.Close() //gosec:disable -- 错误忽略：测试清理
			wg.Wait()
		}()
	})

	client := &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, ln.Addr().String())
			},
		},
		Timeout: 15 * time.Second,
	}
	reqErr := make(chan error, 1)
	go func() {
		resp, err := client.Get("http://inflight.example.org/")
		if err == nil {
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close() //gosec:disable -- 错误忽略：测试清理
		}
		reqErr <- err
	}()

	select {
	case <-inflight:
	case <-time.After(5 * time.Second):
		t.Fatal("5s 内没有建立起在途流：夹具没到位，本用例没有测到关停时的在途流")
	}

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		_ = srv.Close() //gosec:disable -- 错误忽略：本用例就是来测它
		done <- time.Since(start)
	}()
	select {
	case elapsed := <-done:
		t.Logf("在途流场景的关停耗时：%v", elapsed)
	case <-time.After(3 * time.Second):
		t.Fatal("Close 超过 3s 未返回：在途的 h2 流没有被收掉，handler 还堵在读响应上")
	}

	select {
	case err := <-reqErr:
		if err == nil {
			t.Fatal("服务端已关停，在途请求却成功返回")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("关停后在途请求既不成功也不报错：客户端那侧还挂着")
	}
}

// TestH2ConnectIsRejectedNotFaked 钉住 README 里写的那条能力边界：
// Go 的 http2 服务端没有 Hijacker，h2 上的 CONNECT 桥不出裸隧道，只能显式 501。
// 判据是"501 且没有派发会话"：少了后半句，实现哪怕悄悄把 CONNECT 当普通请求转发
// （目标端收到一个 CONNECT 方法、回个 400，客户端以为建隧成功）也算绿。
func TestH2ConnectIsRejectedNotFaked(t *testing.T) {
	ctx := t.Context()
	srv := &Server{ctx: ctx, connChan: make(chan tunnel.Conn, 4)}
	h := &h2Handler{srv: srv}

	req, err := http.NewRequest(http.MethodConnect, "http://tunnel.example.org:443/", nil)
	if err != nil {
		t.Fatalf("构造 CONNECT: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("CONNECT over h2 应显式回 501，实际 %d", rec.Code)
	}
	select {
	case c := <-srv.connChan:
		t.Fatalf("CONNECT 被当成普通请求转发了，还派发出了会话 %v", c.Metadata().Address)
	default:
	}
}

// TestHopByHopHeadersAreStripped 守住抄头这一步：目标端按 h1 回的东西常带逐跳头，
// 原样抄进 h2 会让框架报错断流（真实栈那条 64KB 用例就是靠它兜底的）。
// Host 也在名单里：h2 用 :authority，带 Host 头是非法的。
func TestHopByHopHeadersAreStripped(t *testing.T) {
	for _, k := range []string{
		"Connection", "Keep-Alive", "Proxy-Connection", "Proxy-Authenticate",
		"Proxy-Authorization", "TE", "Trailer", "Transfer-Encoding", "Upgrade", "host",
	} {
		if !hopByHopHeader(k) {
			t.Errorf("%q 是逐跳头，必须被剥掉", k)
		}
	}
	for _, k := range []string{"Content-Type", "Content-Length", "X-Trojan-Test", "Date"} {
		if hopByHopHeader(k) {
			t.Errorf("%q 是端到端头，不能被剥掉", k)
		}
	}
}

// TestH2CRequestDirectionHasNoEarlyEOF 钉的是 h1 分支那条同名判据在 h2 上的翻版：
// 请求转发完了，不能提前在请求方向发 EOF。
//
// 这条不是照抄形式——它在本轮的真实栈用例上真的红过：h2 handler 里那句
// 「defer reqWriter.Close()」让请求方向在请求写完后立刻 EOF，而 proxy 的 relay 是
// 「任一方向结束就拆掉整个会话」，于是 64KB 的 chunked 响应被砍成 unexpected EOF、
// 客户端拿到 502。单元里那个"读完请求头就回一小段响应"的假 relay 抓不到它
// （响应太小、赶在拆会话之前写完了），所以这条必须显式观察请求方向。
func TestH2CRequestDirectionHasNoEarlyEOF(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	srv, err := NewServer(context.Background(), &listenServer{ln: ln})
	if err != nil {
		ln.Close() //gosec:disable -- 错误忽略：测试清理
		t.Fatalf("NewServer: %v", err)
	}

	var once sync.Once
	sessionSeen := make(chan struct{})
	reqRead := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			conn, err := srv.AcceptConn(&Tunnel{})
			if err != nil {
				return
			}
			wg.Go(func() {
				defer conn.Close()
				once.Do(func() { close(sessionSeen) })
				if err := readHeaders(conn); err != nil {
					reqRead <- err
					return
				}
				// 请求头读完了。接下来这一次读就是"请求方向有没有被提前收尾"的探针：
				// 真实 relay 会像 proxy.relayConnLoop 那样把任何 EOF/错误当成整个会话结束。
				_, err := conn.Read(make([]byte, 1))
				reqRead <- err
			})
		}
	})
	t.Cleanup(func() {
		go func() {
			srv.Close() //gosec:disable -- 错误忽略：测试清理
			wg.Wait()
		}()
	})

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, ln.Addr().String())
			},
		},
	}
	go func() {
		resp, err := client.Get("http://eof.example.org/") //gosec:disable -- 错误忽略：本用例只看请求方向
		if err == nil {
			resp.Body.Close() //gosec:disable -- 错误忽略：测试清理
		}
	}()

	select {
	case <-sessionSeen:
	case <-time.After(5 * time.Second):
		t.Fatal("5s 内没有派发转发会话：h2 请求没进 relay，探针没装上")
	}

	select {
	case err := <-reqRead:
		if err == io.EOF {
			t.Fatal("请求方向在响应开始前就发出了 EOF：relay 会把它当会话结束、把还在路上的响应砍断")
		}
		if err != nil {
			t.Fatalf("请求方向提前报错（不是等到会话结束）：%v", err)
		}
		t.Fatal("请求方向在会话结束前还读得到数据？探针逻辑不对")
	case <-time.After(1500 * time.Millisecond):
		// 一直阻塞 = 请求方向没有提前收尾，符合预期
	}
}

// TestH2CRequestBodyIsFullyForwarded 补的是请求方向的盲区：前面几条用的都是没有
// body 的 GET，而代理最容易坏在"带体的请求"上——请求体要跨多个 h2 帧、还要经
// io.Pipe 一段段推给 relay，任何一段被当成会话结束都会截断。
//
// 体积取 512KB：远大于 h2 默认帧(16KB)与 io.Pipe 的单次交接，逼迫真正走流式。
func TestH2CRequestBodyIsFullyForwarded(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	srv, err := NewServer(context.Background(), &listenServer{ln: ln})
	if err != nil {
		ln.Close() //gosec:disable -- 错误忽略：测试清理
		t.Fatalf("NewServer: %v", err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			conn, err := srv.AcceptConn(&Tunnel{})
			if err != nil {
				return
			}
			wg.Go(func() {
				defer conn.Close()
				head, err := readHeadersToString(conn)
				if err != nil {
					return
				}
				body := make([]byte, 0)
				if cl := contentLengthOf(head); cl > 0 {
					body = make([]byte, cl)
					if _, err := io.ReadFull(conn, body); err != nil {
						return
					}
				}
				respHead := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n", len(body))
				if _, err := conn.Write(append([]byte(respHead), body...)); err != nil {
					return
				}
			})
		}
	})
	t.Cleanup(func() {
		go func() {
			srv.Close() //gosec:disable -- 错误忽略：测试清理
			wg.Wait()
		}()
	})

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, ln.Addr().String())
			},
		},
	}
	const size = 512 * 1024
	payload := strings.Repeat("B", size)
	resp, err := client.Post("http://body.example.org/upload", "application/octet-stream", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("带 body 的 h2c 请求失败（没开全双工时就会卡在这里）: %v", err)
	}
	defer resp.Body.Close() //gosec:disable -- 错误忽略：测试清理
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读回响应失败: %v", err)
	}
	if len(got) != size {
		t.Fatalf("请求体只往返了 %d 字节，期望 %d：请求方向被截断了", len(got), size)
	}
	if string(got) != payload {
		t.Fatal("请求体内容被改坏")
	}
}

// readHeadersToString 与 readHeaders 同样按头部边界收，但把读到的头部原文带回来，
// 好让上面的假 relay 知道 body 有多长。
func readHeadersToString(r io.Reader) (string, error) {
	var sb strings.Builder
	one := make([]byte, 1)
	for sb.Len() < 8192 {
		n, err := r.Read(one)
		if n > 0 {
			sb.Write(one[:n])
			if strings.HasSuffix(sb.String(), "\r\n\r\n") {
				return sb.String(), nil
			}
		}
		if err != nil {
			return sb.String(), err
		}
	}
	return sb.String(), fmt.Errorf("请求头没有以 CRLFCRLF 结束")
}

func contentLengthOf(head string) int {
	for line := range strings.SplitSeq(head, "\r\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return 0
			}
			return n
		}
	}
	return 0
}

// throttleReader 每次最多交出一小截、且强制歇一拍，用来把"请求体还在流"这件事
// 拉长到能被观察到（一次交完的话客户端早就写完了，现场就没造出来）。
type throttleReader struct {
	chunk  []byte
	remain int
}

func (r *throttleReader) Read(p []byte) (int, error) {
	if r.remain <= 0 {
		return 0, io.EOF
	}
	n := min(len(p), r.remain, len(r.chunk))
	time.Sleep(time.Millisecond)
	copy(p[:n], r.chunk[:n])
	r.remain -= n
	return n, nil
}

// TestH2CEarlyResponseWithBodyInFlight 造的是代理必须能接住的一类现场：目标端
// 还没等请求体传完就把响应回了（413/400/重定向都算），而客户端还在继续上传。
// 这时 handler 是"带着未消费完的请求体"返回的，任何"写响应前必须先把 body 读完"
// 的实现都会把响应堵死。
//
// 这条同时兼任那句被删掉的 EnableFullDuplex 的记账：本版本实测加不加都在 1ms 内
// 送达（所以那句调用是多余的，已删）；将来若改用 stdlib 的 http.Server 把它带回来，
// 判据就是这一条。
func TestH2CEarlyResponseWithBodyInFlight(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	srv, err := NewServer(context.Background(), &listenServer{ln: ln})
	if err != nil {
		ln.Close() //gosec:disable -- 错误忽略：测试清理
		t.Fatalf("NewServer: %v", err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			conn, err := srv.AcceptConn(&Tunnel{})
			if err != nil {
				return
			}
			wg.Go(func() {
				defer conn.Close()
				if _, err := readHeadersToString(conn); err != nil {
					return
				}
				// 请求体一个字节都不读，直接把响应回掉
				conn.Write([]byte("HTTP/1.1 413 Payload Too Large\r\nContent-Length: 4\r\n\r\nnope")) //gosec:disable -- 错误忽略：假 relay
			})
		}
	})
	t.Cleanup(func() {
		go func() {
			srv.Close() //gosec:disable -- 错误忽略：测试清理
			wg.Wait()
		}()
	})

	client := &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, ln.Addr().String())
			},
		},
	}
	body := &throttleReader{chunk: []byte(strings.Repeat("C", 4096)), remain: 512 * 1024}
	start := time.Now()
	resp, err := client.Post("http://early.example.org/upload", "application/octet-stream", body)
	if err != nil {
		t.Fatalf("目标端提前回响应时请求失败（响应被请求体堵住了）: %v", err)
	}
	defer resp.Body.Close() //gosec:disable -- 错误忽略：测试清理
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读提前回来的响应失败: %v", err)
	}
	took := time.Since(start)
	if resp.StatusCode != http.StatusRequestEntityTooLarge || string(got) != "nope" {
		t.Fatalf("响应不对：status=%d body=%q", resp.StatusCode, string(got))
	}
	if took > 5*time.Second {
		t.Fatalf("响应倒是送到了，但花了 %v：请求体还在流时写响应被拖住了", took)
	}
	t.Logf("请求体还在流时拿回提前响应，耗时 %v", took)
}

// TestH1StillWorksNextToH2Sniff 是反向护栏：嗅探加在头部预算之前，绝不能把
// 普通 h1 请求一起挡掉（这里连发两个请求，顺带覆盖 keep-alive）。
func TestH1StillWorksNextToH2Sniff(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	srv, err := NewServer(context.Background(), &listenServer{ln: ln})
	if err != nil {
		ln.Close() //gosec:disable -- 错误忽略：测试清理
		t.Fatalf("NewServer: %v", err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			conn, err := srv.AcceptConn(&Tunnel{})
			if err != nil {
				return
			}
			wg.Go(func() {
				defer conn.Close()
				for range 2 {
					if err := readHeaders(conn); err != nil {
						return
					}
					const resp = "HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n"
					if _, err := conn.Write([]byte(resp)); err != nil {
						return
					}
				}
			})
		}
	})
	defer func() {
		srv.Close() //gosec:disable -- 错误忽略：测试清理
		wg.Wait()
	}()

	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer raw.Close() //gosec:disable -- 错误忽略：测试清理
	br := bufio.NewReader(raw)
	for i := range 2 {
		req := "GET http://h1.example.org/a HTTP/1.1\r\nHost: h1.example.org\r\n\r\n"
		if _, err := io.WriteString(raw, req); err != nil {
			t.Fatalf("写请求失败: %v", err)
		}
		_ = raw.SetReadDeadline(time.Now().Add(15 * time.Second))
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatalf("第 %d 个 h1 请求读响应失败: %v", i+1, err)
		}
		resp.Body.Close() //gosec:disable -- 错误忽略：测试清理
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("第 %d 个 h1 请求状态码 %d", i+1, resp.StatusCode)
		}
	}
}
