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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
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
		}()
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	redir := NewRedirector(ctx)
	var wg sync.WaitGroup

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			redir.Redirect(&Redirection{
				Dial:        nil,
				RedirectTo:  nil,
				InboundConn: nil,
			})
		}()
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond)
	t.Logf("Channel full test passed")
}
