package http

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// startRecordingServer 起一个会把每条转发会话的目的地报出来的 http 入站。
// "目的地"就是这些判据要看的东西：没有它，"请求被拒了"和"请求被转发到本机"
// 在客户端侧看起来是一样的（都会断连）。
func startRecordingServer(t *testing.T) (addr string, targets chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	common.Must(err)
	srv, err := NewServer(context.Background(), &listenServer{ln: ln})
	common.Must(err)
	targets = make(chan string, 16)
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			conn, err := srv.AcceptConn(&Tunnel{})
			if err != nil {
				return
			}
			targets <- conn.Metadata().Address.String()
			wg.Go(func() {
				defer conn.Close()
				if err := readHeaders(conn); err != nil {
					return
				}
				conn.Write([]byte("HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n")) //gosec:disable -- 错误忽略：假 relay
			})
		}
	})
	t.Cleanup(func() {
		go func() {
			srv.Close() //gosec:disable -- 错误忽略：测试清理
			wg.Wait()
		}()
	})
	return ln.Addr().String(), targets
}

// TestH1RequestWithoutTargetHostIsRejected 钉的是空主机名那条退化路径。
// HTTP/1.0 请求可以不带 Host，此时旧实现落到 NewAddressFromHostPort("",80)，
// 目的地字符串是 ":80" —— 实测 net.Dial("tcp", ":80") **连的是 127.0.0.1**，
// 于是这个代理入口变成"替请求方访问代理自己的本地服务"的通道，
// 而按域名匹配的路由规则一条都不会命中（域名是空串）。
// 判据两头都要看：客户端必须收到 400，并且一条会话都不许派发出去。
func TestH1RequestWithoutTargetHostIsRejected(t *testing.T) {
	addr, targets := startRecordingServer(t)
	conn, err := net.Dial("tcp", addr)
	common.Must(err)
	defer conn.Close() //gosec:disable -- 错误忽略：测试清理

	_, err = io.WriteString(conn, "GET /x HTTP/1.0\r\nUser-Agent: probe\r\n\r\n")
	common.Must(err)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("没有读到响应: %v", err)
	}
	defer resp.Body.Close() //gosec:disable -- 错误忽略：测试清理
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("缺目标主机名的请求应当回 400，实际 %d", resp.StatusCode)
	}
	select {
	case got := <-targets:
		t.Fatalf("没有目标的请求被派发成了转发会话，目的地 = %q（空主机名会拨到本机）", got)
	case <-time.After(800 * time.Millisecond):
	}
}

// TestH1HostWithoutPortDefaultsTo80 是被修的那条 fallback 的**正当用途**：
// 代理请求里 `Host: example.com` 不带端口是合法的，必须按 80 转发。
// 这条在修复前后都得绿——它是防"下一轮收紧过头"的对照。
func TestH1HostWithoutPortDefaultsTo80(t *testing.T) {
	addr, targets := startRecordingServer(t)
	conn, err := net.Dial("tcp", addr)
	common.Must(err)
	defer conn.Close() //gosec:disable -- 错误忽略：测试清理

	_, err = io.WriteString(conn, "GET /x HTTP/1.1\r\nHost: plain.example\r\n\r\n")
	common.Must(err)
	_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("没有读到响应: %v", err)
	}
	defer resp.Body.Close() //gosec:disable -- 错误忽略：测试清理
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("合法请求被拒了：status=%d", resp.StatusCode)
	}
	select {
	case got := <-targets:
		if got != "plain.example:80" {
			t.Fatalf("目的地 = %q，期望 plain.example:80", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("合法请求没有派发转发会话")
	}
}

// TestH2RequestWithoutAuthorityIsRejected 是同一条规则在 h2c 分支上的落点。
// h2 用 :authority 表达目标，缺它就等于没有目的地 —— 直接打 handler，
// 免得依赖框架自身的协议检查（那种绿测的是 stdlib，不是我这段代码）。
func TestH2RequestWithoutAuthorityIsRejected(t *testing.T) {
	ctx := t.Context()
	srv := &Server{ctx: ctx, connChan: make(chan tunnel.Conn, 4)}
	h := &h2Handler{srv: srv}

	req, err := http.NewRequest(http.MethodGet, "http://ignored.example/x", nil)
	common.Must(err)
	req.Host = "" // 模拟缺 :authority
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("缺 :authority 的 h2 请求应当回 400，实际 %d", rec.Code)
	}
	select {
	case c := <-srv.connChan:
		t.Fatalf("没有目标的 h2 请求被派发成了会话，目的地 = %v", c.Metadata().Address)
	default:
	}
}

// TestResolveProxyTargetTable 把解析规则本身钉死，包括三条边界：
// 带端口 / 不带端口 / 空。三个 h1、h2 分支共用这一个函数，分叉不了。
func TestResolveProxyTargetTable(t *testing.T) {
	cases := []struct {
		host    string
		want    string
		wantErr bool
	}{
		{"example.com:8080", "example.com:8080", false},
		{"example.com", "example.com:80", false},
		{"127.0.0.1:6379", "127.0.0.1:6379", false},
		{"", ":80", true}, // 旧行为会给出这个目的地，正是它把代理变成访问本机
	}
	for _, c := range cases {
		addr, err := resolveProxyTarget(c.host)
		if c.wantErr {
			if err == nil {
				t.Fatalf("%q 应当被拒绝，实际给出目的地 %q", c.host, addr.String())
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q 解析失败: %v", c.host, err)
		}
		if addr.String() != c.want {
			t.Fatalf("%q -> %q，期望 %q", c.host, addr.String(), c.want)
		}
	}
}
