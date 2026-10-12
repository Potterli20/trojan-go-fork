package scenario

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

// h2cClient 造一个"先前知识"h2c 客户端：明文直连客户端的入站端口，
// 无论请求写到哪个主机名，TCP 都落到代理入口（与浏览器对明文代理开 proxy-http2 一致）。
func h2cClient(localPort int) *http.Client {
	return &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, fmt.Sprintf("127.0.0.1:%d", localPort))
			},
		},
	}
}

// TestHTTPProxyServesH2COverRealStack 在完整链路（h2c 入站 → trojan → 源站）上
// 守住 h2 代理请求的两件事：响应体一个字节不少，以及一条连接上多条流真能并发。
//
// 为什么必须在真实栈上跑：单元侧那个假 relay 只证明"流变成了会话"，而
// 「任一方向结束就拆会话」「逐跳头不能带进 h2」这两类缺陷，只有让响应真的从
// 源站一路推回来才暴露得出来（上一轮 64KB 截断就是这么发现的）。
// 源站是边写边 flush 的，所以它回的是 chunked h1 响应：Transfer-Encoding 这类
// 逐跳头一旦原样抄进 h2 响应，框架会直接报错断流，客户端读到的 body 就会短。
func TestHTTPProxyServesH2COverRealStack(t *testing.T) {
	o := startOrigin(t, 1) // 64KB
	defer o.stop()
	clientProxy, server, localPort := startPair(t, o)
	defer server.Close() //gosec:disable -- 错误忽略：测试清理
	defer clientProxy.Close()

	client := h2cClient(localPort)

	get := func(path string) (int, int, []byte, error) {
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s%s", o.port, path))
		if err != nil {
			return 0, 0, nil, err
		}
		defer resp.Body.Close() //gosec:disable -- 错误忽略：测试清理
		body, err := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.ProtoMajor, body, err
	}

	status, protoMajor, body, err := get("/one")
	if err != nil {
		t.Fatalf("h2c 请求失败: %v", err)
	}
	if protoMajor != 2 {
		t.Fatalf("响应不是 HTTP/2 拿到的（ProtoMajor=%d）：前文嗅探在真实栈上没生效", protoMajor)
	}
	if status != http.StatusOK || len(body) != 64*1024 {
		t.Fatalf("单流响应不对：status=%d len=%d（期望 65536）", status, len(body))
	}

	// 同一连接上并发三条流：每条都必须拿到完整的 64KB
	const n = 3
	type res struct {
		n   int
		err error
	}
	out := make(chan res, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			_, _, body, err := get(fmt.Sprintf("/c%d", i))
			out <- res{len(body), err}
		})
	}
	wg.Wait()
	close(out)
	for r := range out {
		if r.err != nil {
			t.Fatalf("并发流请求失败: %v", r.err)
		}
		if r.n != 64*1024 {
			t.Fatalf("并发流里有一条只拿到 %d 字节（期望 65536）：多路复用互相踩了", r.n)
		}
	}
}

// TestHTTPShutdownIsNotStalledByIdleH2ConnOverRealStack 把单元侧那条关停唤醒搬到
// 真实栈：入站是一条空闲的 h2c 连接（客户端连接池里还留着），关停必须快。
//
// 这条不是重复劳动：单元里 underlay 是一层薄薄的 TCP 包装，而真实栈的入站连接在
// trojan/mux 之下，"关掉裸连接能不能唤醒卡在读帧上的 ServeConn"依赖下层的
// SetReadDeadline/Close 真的生效。
func TestHTTPShutdownIsNotStalledByIdleH2ConnOverRealStack(t *testing.T) {
	o := startOrigin(t, 1)
	defer o.stop()
	clientProxy, server, localPort := startPair(t, o)
	defer server.Close() //gosec:disable -- 错误忽略：测试清理
	defer clientProxy.Close()

	client := h2cClient(localPort)
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s/idle", o.port))
	if err != nil {
		t.Fatalf("建立 h2c 连接失败: %v", err)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		resp.Body.Close() //gosec:disable -- 错误忽略：测试清理
		t.Fatalf("读干响应失败: %v", err)
	}
	resp.Body.Close() //gosec:disable -- 错误忽略：测试清理
	// 此刻连接空闲且留在连接池里：关停要唤醒的正是这条还活着、但没人收发的连接

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		clientProxy.Close() //gosec:disable -- 错误忽略：耗时才是本用例的重点
		done <- time.Since(start)
	}()
	select {
	case elapsed := <-done:
		t.Logf("空闲 h2c 连接下的客户端关停耗时：%v", elapsed)
		if elapsed > 3*time.Second {
			t.Fatalf("关停耗时 %v：空闲的 h2c 入站连接仍要靠兜底超时掐断", elapsed)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("关停 20s 仍未返回：入站 handler 卡在 h2 读帧上，无人唤醒")
	}
}
