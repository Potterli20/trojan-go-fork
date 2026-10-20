package http

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// failingBody 模拟"请求体读到一半就失败"的 h2 客户端：读立即报错。
type failingBody struct{ err error }

func (b *failingBody) Read([]byte) (int, error) { return 0, b.err }
func (b *failingBody) Close() error             { return nil }

// TestH2RequestBodyPumpFailureTearsDownStream 盯的是 h2 请求体泵的失败出口：
// 客户端中途放弃上传后 r.Write 报错，如果这条路径不关 reqWriter，relay 的
// OtherConn.Read 就永远阻塞，整条会话（两个中继协程 + 管道 + ctx）留到 Server 关停，
// 而 ServeHTTP 也阻塞在读响应上，defer stream.Close() 永远轮不到。
//
// 判据是"有限时间内拿到错误"，不是"日志里有没有一行 Error"——旧实现照样打日志，
// 只是把会话留在那里。
func TestH2RequestBodyPumpFailureTearsDownStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := &Server{
		ctx:      ctx,
		cancel:   cancel,
		connChan: make(chan tunnel.Conn, 1),
	}
	rawClient, rawServer := net.Pipe()
	defer rawClient.Close() //gosec:disable -- 错误忽略：测试清理
	defer rawServer.Close() //gosec:disable -- 错误忽略：测试清理

	h := &h2Handler{srv: srv, conn: &pipeConn{Conn: rawServer}}
	req := &http.Request{
		Method:        http.MethodPost,
		URL:           &url.URL{Path: "/upload"},
		Host:          "target.example.org",
		Header:        make(http.Header),
		Body:          &failingBody{err: errors.New("client aborted the upload")},
		ContentLength: 16,
	}
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()

	var stream tunnel.Conn
	select {
	case stream = <-srv.connChan:
	case <-time.After(3 * time.Second):
		t.Fatal("ServeHTTP 没有把会话交给 relay")
	}
	// 失败路径上也要收尾 ServeHTTP，否则这条用例自己会留下一个挂住的协程
	defer func() {
		stream.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Errorf("会话关掉之后 ServeHTTP 仍未返回")
		}
	}()

	readErr := make(chan error, 1)
	go func() {
		// 泵在失败前已经写进了请求头，所以要一直读到管道报错为止
		buf := make([]byte, 512)
		for {
			if _, err := stream.Read(buf); err != nil {
				readErr <- err
				return
			}
		}
	}()
	select {
	case err := <-readErr:
		if err == nil {
			t.Fatal("请求体泵失败后 relay 侧读到 nil error，期望管道被带原因关闭")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("请求体泵失败后 relay 读仍阻塞：reqWriter 没有关闭，会话泄露")
	}
}
