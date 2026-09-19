package transport

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/test/util"
	"github.com/Potterli20/trojan-go-fork/tunnel/freedom"
)

func TestTransport(t *testing.T) {
	serverCfg := &Config{
		LocalHost:  "127.0.0.1",
		LocalPort:  common.PickPort("tcp", "127.0.0.1"),
		RemoteHost: "127.0.0.1",
		RemotePort: common.PickPort("tcp", "127.0.0.1"),
	}
	clientCfg := &Config{
		LocalHost:  "127.0.0.1",
		LocalPort:  common.PickPort("tcp", "127.0.0.1"),
		RemoteHost: "127.0.0.1",
		RemotePort: serverCfg.LocalPort,
	}
	freedomCfg := &freedom.Config{}
	sctx := config.WithConfig(context.Background(), Name, serverCfg)
	cctx := config.WithConfig(context.Background(), Name, clientCfg)
	cctx = config.WithConfig(cctx, freedom.Name, freedomCfg)

	s, err := NewServer(sctx, nil)
	common.Must(err)
	c, err := NewClient(cctx, nil)
	common.Must(err)

	wg := sync.WaitGroup{}
	var conn1, conn2 net.Conn
	wg.Go(func() {
		var acceptErr error
		conn2, acceptErr = s.AcceptConn(nil)
		common.Must(acceptErr)
	})
	conn1, err = c.DialConn(nil, nil)
	common.Must(err)

	common.Must2(conn1.Write([]byte("12345678\r\n")))
	wg.Wait()
	buf := [10]byte{}
	conn2.Read(buf[:])
	if !util.CheckConn(conn1, conn2) {
		t.Fail()
	}
	s.Close()
	c.Close()
}

func TestClientPlugin(t *testing.T) {
	clientCfg := &Config{
		LocalHost:  "127.0.0.1",
		LocalPort:  common.PickPort("tcp", "127.0.0.1"),
		RemoteHost: "127.0.0.1",
		RemotePort: 12345,
		TransportPlugin: TransportPluginConfig{
			Enabled: true,
			Type:    "shadowsocks",
			Command: "sh",
			Option:  "",
			Arg:     []string{"-c", "echo $SS_REMOTE_PORT"},
			Env:     nil,
		},
	}
	ctx := config.WithConfig(context.Background(), Name, clientCfg)
	freedomCfg := &freedom.Config{}
	ctx = config.WithConfig(ctx, freedom.Name, freedomCfg)
	c, err := NewClient(ctx, nil)
	common.Must(err)
	c.Close()
}

func TestServerPlugin(t *testing.T) {
	cfg := &Config{
		LocalHost:  "127.0.0.1",
		LocalPort:  common.PickPort("tcp", "127.0.0.1"),
		RemoteHost: "127.0.0.1",
		RemotePort: 12345,
		TransportPlugin: TransportPluginConfig{
			Enabled: true,
			Type:    "shadowsocks",
			Command: "sh",
			Option:  "",
			Arg:     []string{"-c", "echo $SS_REMOTE_PORT"},
			Env:     nil,
		},
	}
	ctx := config.WithConfig(context.Background(), Name, cfg)
	freedomCfg := &freedom.Config{}
	ctx = config.WithConfig(ctx, freedom.Name, freedomCfg)
	s, err := NewServer(ctx, nil)
	common.Must(err)
	s.Close()
}

// 关闭服务端时必须排空 connChan/wsChan 里未被取走的连接，
// 否则它们带着 fd 一直滞留到进程结束（与 tls 层同型泄露）
func TestServerCloseDrainsQueuedConns(t *testing.T) {
	cfg := &Config{
		LocalHost:  "127.0.0.1",
		LocalPort:  common.PickPort("tcp", "127.0.0.1"),
		RemoteHost: "127.0.0.1",
		RemotePort: common.PickPort("tcp", "127.0.0.1"),
	}
	ctx := config.WithConfig(context.Background(), Name, cfg)
	s, err := NewServer(ctx, nil)
	common.Must(err)

	queued, peer := net.Pipe()
	s.connChan <- &Conn{Conn: queued}
	if len(s.connChan) != 1 {
		t.Fatal("connection not queued")
	}

	common.Must(s.Close())

	if len(s.connChan) != 0 {
		t.Fatalf("connChan still holds %d connections after Close", len(s.connChan))
	}
	_ = peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := peer.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("queued connection was not closed on shutdown, got %v", err)
	}
	peer.Close() //gosec:disable -- 测试清理，忽略 close 错误
}
