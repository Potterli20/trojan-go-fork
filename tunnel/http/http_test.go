package http

import (
	"bufio"
	"context"
	"fmt"
	"io"

	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/test/util"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/Potterli20/trojan-go-fork/tunnel/transport"
)

func TestHTTP(t *testing.T) {
	port := common.PickPort("tcp", "127.0.0.1")
	ctx := config.WithConfig(context.Background(), transport.Name, &transport.Config{
		LocalHost: "127.0.0.1",
		LocalPort: port,
	})

	tcpServer, err := transport.NewServer(ctx, nil)
	common.Must(err)
	s, err := NewServer(ctx, tcpServer)
	common.Must(err)

	for range 10 {
		go func() {
			resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d", port))
			common.Must(err)
			defer resp.Body.Close()
		}()
		time.Sleep(time.Microsecond * 10)
		conn, err := s.AcceptConn(nil)
		common.Must(err)
		bufReader := bufio.NewReader(bufio.NewReader(conn))
		req, err := http.ReadRequest(bufReader)
		common.Must(err)
		fmt.Println(req)
		io.ReadAll(req.Body)
		req.Body.Close()
		resp, err := http.Get("http://127.0.0.1:" + util.HTTPPort)
		common.Must(err)
		defer resp.Body.Close()
		err = resp.Write(conn)
		common.Must(err)
		buf := [100]byte{}
		_, err = conn.Read(buf[:])
		if err == nil {
			t.Fail()
		}
		conn.Close()
	}

	req, err := http.NewRequest(http.MethodConnect, "https://google.com:443", nil)
	common.Must(err)
	conn1, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	common.Must(err)
	go func() {
		common.Must(req.Write(conn1))
	}()

	conn2, err := s.AcceptConn(nil)
	common.Must(err)

	if conn2.Metadata().Port != 443 || conn2.Metadata().DomainName != "google.com" {
		t.Fail()
	}

	connResp := "HTTP/1.1 200 Connection established\r\n\r\n"
	buf := make([]byte, len(connResp))
	_, err = conn1.Read(buf)
	common.Must(err)
	if string(buf) != connResp {
		t.Fail()
	}

	if !util.CheckConn(conn1, conn2) {
		t.Fail()
	}

	conn1.Close()
	conn2.Close()
	s.Close()
}

// blockingUnderlay 的 AcceptConn 阻塞到 Close 为止，避免 acceptLoop 空转刷日志
type blockingUnderlay struct {
	done chan struct{}
}

func (u *blockingUnderlay) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	<-u.done
	return nil, common.NewError("underlay closed")
}

func (u *blockingUnderlay) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	<-u.done
	return nil, common.NewError("underlay closed")
}

func (u *blockingUnderlay) Close() error {
	close(u.done)
	return nil
}

// TestTunnelNewServerWithoutConfig 复现提交 73a7a822 引入的启动崩溃：HTTP 从未注册
// config creator，config.FromContext 恒为 nil，裸类型断言在栈构建阶段 panic，
// 使所有含 http 入站的 client 模式无法启动。
// 现在 NewServer 已经不读 ctx 配置，这类崩溃在结构上不可能再发生；留着本用例是为了
// 万一有人重新往里塞 config.FromContext(ctx, Name).(*Xxx) 的裸断言时能当场被测出来。
func TestTunnelNewServerWithoutConfig(t *testing.T) {
	underlay := &blockingUnderlay{done: make(chan struct{})}
	srv, err := (&Tunnel{}).NewServer(context.Background(), underlay)
	if err != nil {
		t.Fatalf("NewServer with no config registered should succeed, got %v", err)
	}
	if srv == nil {
		t.Fatal("NewServer returned a nil server")
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}
