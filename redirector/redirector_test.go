package redirector

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/test/util"
)

func TestRedirector(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	redir := NewRedirector(ctx)
	redir.Redirect(&Redirection{
		Dial:        nil,
		RedirectTo:  nil,
		InboundConn: nil,
	})
	var fakeAddr net.Addr
	var fakeConn net.Conn
	redir.Redirect(&Redirection{
		Dial:        nil,
		RedirectTo:  fakeAddr,
		InboundConn: fakeConn,
	})
	redir.Redirect(&Redirection{
		Dial:        nil,
		RedirectTo:  nil,
		InboundConn: fakeConn,
	})
	redir.Redirect(&Redirection{
		Dial:        nil,
		RedirectTo:  fakeAddr,
		InboundConn: nil,
	})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	common.Must(err)
	conn1, err := net.Dial("tcp", l.Addr().String())
	common.Must(err)
	conn2, err := l.Accept()
	common.Must(err)
	redirAddr, err := net.ResolveTCPAddr("tcp", util.HTTPAddr)
	common.Must(err)
	redir.Redirect(&Redirection{
		Dial:        nil,
		RedirectTo:  redirAddr,
		InboundConn: conn2,
	})
	time.Sleep(time.Second)
	req, err := http.NewRequest("GET", "http://localhost/", nil)
	common.Must(err)
	req.Write(conn1)
	buf := make([]byte, 1024)
	conn1.Read(buf)
	fmt.Println(string(buf))
	if !strings.HasPrefix(string(buf), "HTTP/1.1 200 OK") {
		t.Fail()
	}
	cancel()
	conn1.Close()
	conn2.Close()
}

func TestRedirectorClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	redir := NewRedirector(ctx)

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	err := redir.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestRedirectorConcurrentRedirection(t *testing.T) {
	const numGoroutines = 100
	ctx := t.Context()

	redir := NewRedirector(ctx)
	var wg sync.WaitGroup
	var redirectCount atomic.Int32

	l, err := net.Listen("tcp", "127.0.0.1:0")
	common.Must(err)
	defer l.Close()

	go func() {
		for {
			_, err := l.Accept()
			if err != nil {
				return
			}
		}
	}()

	redirAddr, err := net.ResolveTCPAddr("tcp", l.Addr().String())
	common.Must(err)

	for range numGoroutines {
		wg.Go(func() {
			conn1, err := net.Dial("tcp", l.Addr().String())
			if err != nil {
				return
			}
			redir.Redirect(&Redirection{
				Dial: func(addr net.Addr) (net.Conn, error) {
					redirectCount.Add(1)
					return net.Dial("tcp", addr.String())
				},
				RedirectTo:  redirAddr,
				InboundConn: conn1,
			})
		})
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for concurrent redirections")
	}

	t.Logf("Concurrent redirect test completed")
}

func TestRedirectorBoundaryConditions(t *testing.T) {
	testCases := []struct {
		name        string
		redirection *Redirection
		expectPanic bool
	}{
		{
			name: "All nil",
			redirection: &Redirection{
				Dial:        nil,
				RedirectTo:  nil,
				InboundConn: nil,
			},
			expectPanic: false,
		},
		{
			name: "Nil InboundConn",
			redirection: &Redirection{
				Dial:        func(addr net.Addr) (net.Conn, error) { return nil, nil },
				RedirectTo:  &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 80},
				InboundConn: nil,
			},
			expectPanic: false,
		},
		{
			name: "Nil RedirectTo",
			redirection: &Redirection{
				Dial:        nil,
				RedirectTo:  nil,
				InboundConn: &net.TCPConn{},
			},
			expectPanic: false,
		},
		{
			name: "Nil Dial",
			redirection: &Redirection{
				Dial:        nil,
				RedirectTo:  &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 80},
				InboundConn: &net.TCPConn{},
			},
			expectPanic: false,
		},
	}

	ctx := t.Context()
	redir := NewRedirector(ctx)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); (r != nil) != tc.expectPanic {
					t.Errorf("test %q panic = %v, expectPanic = %v", tc.name, r, tc.expectPanic)
				}
			}()
			redir.Redirect(tc.redirection)
		})
	}
}

func TestRedirectorChannelFull(t *testing.T) {
	ctx := t.Context()

	redir := NewRedirector(ctx)
	var wg sync.WaitGroup

	for range 200 {
		wg.Go(func() {
			redir.Redirect(&Redirection{
				Dial:        nil,
				RedirectTo:  nil,
				InboundConn: nil,
			})
		})
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond)
	t.Logf("Channel full test passed")
}

// TestRedirectorIdleTeardown 诱饵路径处理的是认证失败的流量，发送方不需要任何凭据：
// 「连上就静默」的对端曾让两个 io.Copy 永久阻塞，每条连接留下 3 个 goroutine 和 2 个 fd。
// 这里用 synctest 的假时钟验证空闲超时把会话收回：气泡内时间只在所有 goroutine 都
// durably blocked 时才前进，所以测试不占真实墙钟、也不依赖 sleep，不会 flaky。
func TestRedirectorIdleTeardown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// 把硬上限推到 1 小时以外，确保会话只能被「空闲超时」这一条路径回收；
		// 否则 redirectionMaxLife 会误打误撞把连接收掉，空闲逻辑就没被真正检验
		maxLife := redirectionMaxLife
		t.Cleanup(func() { redirectionMaxLife = maxLife })
		redirectionMaxLife = time.Hour

		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		redir := NewRedirector(ctx)

		client, peer := net.Pipe()
		outbound, unused := net.Pipe()
		t.Cleanup(func() {
			client.Close()   //gosec:disable -- 错误忽略：测试清理
			peer.Close()     //gosec:disable -- 错误忽略：测试清理
			outbound.Close() //gosec:disable -- 错误忽略：测试清理
			unused.Close()   //gosec:disable -- 错误忽略：测试清理
		})

		redir.Redirect(&Redirection{
			Dial:        func(net.Addr) (net.Conn, error) { return outbound, nil },
			RedirectTo:  &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 443},
			InboundConn: client,
		})

		// peer 一个字节都不发。假时钟只在所有 goroutine 阻塞时前进，
		// 所以这里测得的时间差就是会话真正被回收时的空龄
		start := time.Now()
		res := make(chan error, 1)
		go func() {
			_, err := peer.Read(make([]byte, 1))
			res <- err
		}()

		giveUp := time.NewTimer(3 * redirectionIdleTimeout)
		defer giveUp.Stop()
		select {
		case err := <-res:
			if err == nil {
				t.Fatal("静默对端的会话仍持有连接，没有被回收")
			}
			if age := time.Since(start); age < redirectionIdleTimeout {
				t.Errorf("会话空龄 %v 就被回收，短于空闲上限 %v：回收不是空闲超时做的",
					age, redirectionIdleTimeout)
			}
		case <-giveUp.C:
			t.Fatal("静默对端的会话没有被空闲超时回收：中继 goroutine 与 2 个 fd 仍在泄漏")
		}
	})
}

// TestRedirectorCloseWithSilentPeer 关键回归：cancel() 不关闭连接时，阻塞在 io.Copy 里的
// 中继永不苏醒。只断言「Close 返回了」并不够——有界排空本身就保证它会返回，
// 真正的泄露是返回之后仍挂着的中继 goroutine 和两个 fd，所以必须验证连接被回收。
func TestRedirectorCloseWithSilentPeer(t *testing.T) {
	ctx := t.Context()
	redir := NewRedirector(ctx)

	client, peer := net.Pipe()
	outbound, unused := net.Pipe()
	defer peer.Close()     //gosec:disable -- 错误忽略：测试清理
	defer unused.Close()   //gosec:disable -- 错误忽略：测试清理
	defer outbound.Close() //gosec:disable -- 错误忽略：测试清理

	redir.Redirect(&Redirection{
		Dial:        func(net.Addr) (net.Conn, error) { return outbound, nil },
		RedirectTo:  &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 443},
		InboundConn: client,
	})

	// 确认会话已进入中继阶段：net.Pipe 同步，Write 返回即说明中继已读走该字节。
	// 之后诱饵侧无人收取，中继阻塞在 Write 上 —— 正是要复现的挂死条件。
	if _, err := peer.Write([]byte("x")); err != nil {
		t.Fatalf("peer write failed: %v", err)
	}

	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- redir.Close() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close() returned error: %v", err)
		}
	case <-time.After(2 * redirectionDrainWait):
		t.Fatal("静默对端存在时 Close() 挂死：cancel 没能解除 io.Copy 的阻塞")
	}

	// 回收必须紧跟 cancel 发生，而不是等满排空上限后靠 worker 的 defer 兜底：
	// 后者意味着中继 goroutine 在整个 redirectionDrainWait 期间仍占着连接和 fd
	if took := time.Since(start); took > redirectionDrainWait/2 {
		t.Errorf("Close() 耗时 %v，已接近排空上限 %v：ctx 取消没有立即回收连接",
			took, redirectionDrainWait)
	}

	// Close 返回后两端必须已关闭
	recycled := make(chan error, 1)
	go func() {
		_, err := peer.Read(make([]byte, 1))
		recycled <- err
	}()
	select {
	case err := <-recycled:
		if err == nil {
			t.Fatal("连接关闭后仍从对端读到数据，回收不符合预期")
		}
	case <-time.After(redirectionDrainWait):
		t.Fatal("Close() 返回但连接未被回收：中继 goroutine 仍在阻塞，fd 泄露")
	}
}
