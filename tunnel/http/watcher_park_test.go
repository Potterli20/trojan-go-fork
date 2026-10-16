package http

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"runtime"
	"testing"
	"time"
)

// goroutineCountAtLeast 轮询直到协程数降到 target 以下，或超时。
// 返回最后一次观测值，用来判断"是回收慢"还是"根本没回收"。
func waitGoroutinesAtMost(t *testing.T, target int) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	cur := runtime.NumGoroutine()
	for cur > target && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		cur = runtime.NumGoroutine()
	}
	return cur
}

// TestOneShotConnectionsDoNotParkWatchers 钉住"每条连接留一个 watcher"这类泄露。
//
// 关停唤醒协程过去只监听 s.ctx，于是它要 park 到整个 Server.Close 才退出：每条连接
// 留一个 goroutine，而且它经由那个 OnceFunc 闭包留住裸连接对象（TLS 的读写缓冲都在
// 里面）。对端只要用"一个请求一条连接"（Connection: close、curl、扫描器），占用就按
// 连接数线性增长，且运行期永不回收——放到公网上就是能被连接洪水放大的常驻增长。
//
// 判据：N 条一次性连接全部走完之后，协程数必须回到基线。裸连接由 handler 的
// defer 关掉，所以这里数的不是 fd，而是 park 着的 watcher。
func TestOneShotConnectionsDoNotParkWatchers(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv, err := NewServer(context.Background(), &listenServer{ln: ln})
	if err != nil {
		ln.Close() //gosec:disable -- 错误忽略：测试清理
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close() //gosec:disable -- 错误忽略：测试清理

	// 假 relay：读掉请求头、回一个 200，然后关会话（只关管道，不关裸连接）
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			conn, err := srv.AcceptConn(&Tunnel{})
			if err != nil {
				return
			}
			br := bufio.NewReader(conn)
			for {
				line, rerr := br.ReadString('\n')
				if rerr != nil {
					break
				}
				if line == "\r\n" {
					break
				}
			}
			_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nhi"))
			conn.Close() //gosec:disable -- 错误忽略：测试清理
		}
	}()
	defer close(stop)

	addr := ln.Addr().String()
	// 先跑一条，把 acceptLoop / 假 relay 这些本用例自己的常驻协程算进基线
	oneShotRequest(t, addr, "warm")
	baseline := runtime.NumGoroutine()

	const rounds = 40
	for i := range rounds {
		oneShotRequest(t, addr, fmt.Sprint(i))
	}

	after := waitGoroutinesAtMost(t, baseline+2)
	if after > baseline+2 {
		t.Fatalf("%d 条一次性连接之后协程数从 %d 涨到 %d 且不回落：每条连接的关停唤醒者都 park 到了 Server.Close",
			rounds, baseline, after)
	}
}

// oneShotRequest 走一条"一个请求就结束"的连接：发完请求立刻 CloseWrite 交出 FIN，
// handler 回完响应、下一次读到 EOF 就结束这一轮，再把服务端剩余字节读干净。
// 不这样做的话 handler 会停在 keep-alive 的下一个请求读上，客户端也等不到 EOF——
// 这个用例的第一版就是这么把自己挂住的。
func oneShotRequest(t *testing.T, addr, path string) {
	t.Helper()
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial %s: %v", path, err)
	}
	c, ok := raw.(*net.TCPConn)
	if !ok {
		raw.Close() //gosec:disable -- 错误忽略：测试清理
		t.Fatalf("期望 *net.TCPConn，得到 %T", raw)
	}
	if err := c.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
	_, _ = fmt.Fprintf(c, "GET http://target.example.org/%s HTTP/1.1\r\nHost: target.example.org\r\nConnection: close\r\n\r\n", path)
	if err := c.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite: %v", err)
	}
	io.Copy(io.Discard, c) //gosec:disable -- 错误忽略：读干净即可，内容不是本用例的断言对象
	c.Close()              //gosec:disable -- 错误忽略：测试清理
}
