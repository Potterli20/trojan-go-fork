package http

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// 这个测试盯的是关停路径上的一个无界等待：handler 拿到响应后走 resp.Write(conn)，
// 把字节写回客户端。若客户端已经不再读取（对端 socket 缓冲满、或像本测试一样用
// net.Pipe 的同步语义），这一句会一直阻塞；而 Server.Close 里的 wg.Wait 没有上限，
// 于是整个关停被这一个连接拖住。
//
// 为什么判据不空转：裸连接上只设过 SetReadDeadline，从未设过写 deadline，所以
// resp.Write 不会"自己"超时返回。Close 能在时限内返回，只有两种可能——修复后的
// 关停逻辑关掉了裸连接，或者别的代码提前关了它；因此除了时限，还额外断言客户端
// 那一端确实看到了连接被关闭。
//
// 时限取 3s：远小于本文件唯一的既有界限 handshakeTimeout=30s，避免"靠 30s 读超时
// 侥幸返回"被误当成通过。
func TestHTTPServerCloseWakesStalledResponseWrite(t *testing.T) {
	client, serverSide := net.Pipe()
	srv, err := NewServer(context.Background(), &pipeServer{conn: &pipeConn{Conn: serverSide}})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// 1. 客户端发一个普通 GET，让 handler 走到 keep-alive 分支
	go func() {
		fmt.Fprint(client, getReq1)
	}()

	conn, err := srv.AcceptConn(&Tunnel{})
	if err != nil {
		t.Fatalf("AcceptConn: %v", err)
	}
	newConn, ok := conn.(*OtherConn)
	if !ok {
		t.Fatalf("期望 *OtherConn，实际 %T", conn)
	}

	// 2. 读走请求，放行 handler 的 req.Write(reqWriter)。
	// 必须按「请求头边界」读满就走，不能读到 EOF：请求方向的 EOF 只在本请求的会话
	// 结束时才发出（提前发 EOF 会让 relay 把还在传输的响应砍断，见 server.go 说明）。
	if err := readHeaders(newConn); err != nil {
		t.Fatalf("读取转发请求失败: %v", err)
	}

	// 3. 喂回一个响应，让 handler 进入 resp.Write(conn)
	const respBytes = "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello"
	if _, err := newConn.Write([]byte(respBytes)); err != nil {
		t.Fatalf("写入响应失败: %v", err)
	}

	// 故意不从 client 端把响应读干：handler 随即卡在写响应上。
	// 先确认它确实卡住了——net.Pipe 是同步的，resp.Write 会把状态行先 flush 出去
	// （实测 17 字节），之后的写入没有读者就一直堵着。所以判据是"把已推出的字节
	// 读干后，再读一次得到 i/o timeout"：既证明连接还开着，也证明写没完成。
	if err := assertWriteIsStuck(client); err != nil {
		t.Fatalf("夹具未复现阻塞现场：%v", err)
	}

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		_ = srv.Close() //gosec:disable -- 错误忽略：测试清理
		done <- time.Since(start)
	}()

	select {
	case elapsed := <-done:
		// 4. 反虚假通过：裸连接必须真的被关掉，而不是"写侥幸完成"
		_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := client.Read(make([]byte, 64))
		if err == nil {
			t.Fatalf("Close 在 %v 内返回了，但客户端仍能读到 %d 字节数据——裸连接没被关掉，"+
				"响应写是自己完成的，本测试没有测到关停唤醒", elapsed, n)
		}
		if isTimeout(err) {
			t.Fatalf("Close 在 %v 内返回了，但客户端读取只得到超时：连接并未关闭", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close 超过 3s 仍未返回：handler 卡在写响应上，关停被单个连接拖住")
	}
}

// acceptWithin 在时限内等待下一个派发的连接。超时时必须自己报错，而不是让测试
// 一直阻塞到包级 -timeout 才失败：那样只剩一条 panic 堆栈，看不出是
// 「第二个请求没被派发」这个真实原因（我第一次做变异实验时就是这么糊过去的）。
func acceptWithin(t *testing.T, srv *Server, d time.Duration) tunnel.Conn {
	t.Helper()
	type res struct {
		conn tunnel.Conn
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := srv.AcceptConn(&Tunnel{})
		ch <- res{c, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("第二个请求 AcceptConn: %v", r.err)
		}
		return r.conn
	case <-time.After(d):
		t.Fatalf("%v 内没有派发第二个请求：keep-alive 已退化成一个请求一条连接", d)
	}
	return nil
}

// 三个请求原文。读请求时用 readHeaders（读到头部边界就走），模拟真实 relay
// 「边读边转发」，而不是傻等一个只在会话结束时才出现的 EOF。
const (
	getReq1       = "GET http://example.com/a HTTP/1.1\r\nHost: example.com\r\n\r\n"
	keepAliveReq1 = "GET http://one.example.com/a HTTP/1.1\r\nHost: one.example.com\r\n\r\n"
	keepAliveReq2 = "GET http://two.example.com/b HTTP/1.1\r\nHost: two.example.com\r\n\r\n"
)

// readHeaders 把转发方向的请求读到 \r\n\r\n 为止。
// 不能拿原文逐字节比对：req.Write 是按 client 侧的写法重新序列化的，绝对形式的路径
// 会变成相对路径，还会补上 User-Agent——这与客户端发来的字节并不相同。
func readHeaders(r io.Reader) error {
	var sb strings.Builder
	one := make([]byte, 1)
	for sb.Len() < 8192 {
		n, err := r.Read(one)
		if n > 0 {
			sb.Write(one[:n])
			if strings.HasSuffix(sb.String(), "\r\n\r\n") {
				return nil
			}
		}
		if err != nil {
			return err
		}
	}
	return fmt.Errorf("请求头没有以 CRLFCRLF 结束：%q", sb.String())
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// assertWriteIsStuck 把客户端已经推出来的字节读干，要求最终一次读得到 i/o timeout。
// timeout 同时说明两件事：连接还开着，且对端的写还堵着。
func assertWriteIsStuck(client net.Conn) error {
	buf := make([]byte, 64)
	for {
		_ = client.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		if _, err := client.Read(buf); err == nil {
			continue
		} else if isTimeout(err) {
			return nil
		} else {
			return fmt.Errorf("客户端读取报错 %v（期望 i/o timeout）", err)
		}
	}
}

// TestHTTPServerKeepAliveSurvivesRequestClose 是我这条修复的反向护栏：
// 单个请求结束时的 newConn.Close() 只关管道，绝不能关裸连接——否则 keep-alive
// 退化成"一条连接只服务一个请求"，比原本的 bug 更糟。
//
// 这条在修复前后都必须是绿的（修复前它就是绿的），否则它只是一条永真判据。
func TestHTTPServerKeepAliveSurvivesRequestClose(t *testing.T) {
	client, serverSide := net.Pipe()
	srv, err := NewServer(context.Background(), &pipeServer{conn: &pipeConn{Conn: serverSide}})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close() //gosec:disable -- 错误忽略：测试清理

	// 第一个请求
	go func() {
		fmt.Fprint(client, keepAliveReq1)
	}()
	first, err := srv.AcceptConn(&Tunnel{})
	if err != nil {
		t.Fatalf("第一个请求 AcceptConn: %v", err)
	}
	if err := readHeaders(first); err != nil {
		t.Fatalf("读取第一个请求: %v", err)
	}
	const respBytes = "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello"
	if _, err := first.Write([]byte(respBytes)); err != nil {
		t.Fatalf("写第一个响应: %v", err)
	}

	// 把响应完整读走，handler 才会回到"等下一个请求"
	_ = client.SetReadDeadline(time.Now().Add(10 * time.Second))
	got := drainUntil(client, len(respBytes))
	if !strings.Contains(got, "200 OK") || !strings.Contains(got, "hello") {
		t.Fatalf("客户端没收到完整响应，实际 %q", got)
	}

	// 第二个请求：连接还活着才能被服务
	go func() {
		fmt.Fprint(client, keepAliveReq2)
	}()
	second := acceptWithin(t, srv, 10*time.Second)
	if addr := second.Metadata().Address; addr == nil || !strings.Contains(addr.String(), "two.example.com") {
		t.Fatalf("第二个请求的目的地不对，实际 %v", second.Metadata().Address)
	}
	if err := readHeaders(second); err != nil {
		t.Fatalf("读取第二个请求: %v", err)
	}
	// 本测试只关心"第二个请求能否被派发"，不回响应。handler 随后停在
	// http.ReadResponse 上等 respReader 的字节，所以这里必须主动关掉这条
	// OtherConn（关闭会让 ReadResponse 报错返回），否则测试自身的清理会挂住——
	// 那是夹具问题，不是被测缺陷。
	second.Close() //gosec:disable -- 错误忽略：测试清理
}

func drainUntil(c net.Conn, min int) string {
	var sb strings.Builder
	buf := make([]byte, 512)
	for sb.Len() < min {
		n, err := c.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return sb.String()
}
