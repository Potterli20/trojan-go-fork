package trojan

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/statistic/memory"
	"github.com/Potterli20/trojan-go-fork/test/util"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/Potterli20/trojan-go-fork/tunnel/freedom"
	"github.com/Potterli20/trojan-go-fork/tunnel/transport"
)

func TestTrojan(t *testing.T) {
	port := common.PickPort("tcp", "127.0.0.1")
	transportConfig := &transport.Config{
		LocalHost:  "127.0.0.1",
		LocalPort:  port,
		RemoteHost: "127.0.0.1",
		RemotePort: port,
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx = config.WithConfig(ctx, transport.Name, transportConfig)
	ctx = config.WithConfig(ctx, freedom.Name, &freedom.Config{})
	tcpClient, err := transport.NewClient(ctx, nil)
	common.Must(err)
	tcpServer, err := transport.NewServer(ctx, nil)
	common.Must(err)

	serverPort := common.PickPort("tcp", "127.0.0.1")
	authConfig := &memory.Config{Passwords: []string{"password"}}
	clientConfig := &Config{
		RemoteHost: "127.0.0.1",
		RemotePort: serverPort,
	}
	serverConfig := &Config{
		LocalHost:  "127.0.0.1",
		LocalPort:  serverPort,
		RemoteHost: "127.0.0.1",
		RemotePort: util.EchoPort,
	}

	ctx = config.WithConfig(ctx, memory.Name, authConfig)
	clientCtx := config.WithConfig(ctx, Name, clientConfig)
	serverCtx := config.WithConfig(ctx, Name, serverConfig)
	c, err := NewClient(clientCtx, tcpClient)
	common.Must(err)
	s, err := NewServer(serverCtx, tcpServer)
	common.Must(err)
	conn1, err := c.DialConn(&tunnel.Address{
		DomainName:  "example.com",
		AddressType: tunnel.DomainName,
	}, nil)
	common.Must(err)
	common.Must2(conn1.Write([]byte("87654321")))
	conn2, err := s.AcceptConn(nil)
	common.Must(err)
	buf := [8]byte{}
	conn2.Read(buf[:])
	if !util.CheckConn(conn1, conn2) {
		t.Fail()
	}

	packet1, err := c.DialPacket(nil)
	common.Must(err)
	packet1.WriteWithMetadata([]byte("12345678"), &tunnel.Metadata{
		Address: &tunnel.Address{
			DomainName:  "example.com",
			AddressType: tunnel.DomainName,
			Port:        80,
		},
	})
	packet2, err := s.AcceptPacket(nil)
	common.Must(err)

	_, m, err := packet2.ReadWithMetadata(buf[:])
	common.Must(err)

	fmt.Println(m)

	if !util.CheckPacketOverConn(packet1, packet2) {
		t.Fail()
	}

	// redirecting
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	common.Must(err)
	sendBuf := util.GeneratePayload(1024)
	recvBuf := [1024]byte{}
	common.Must2(conn.Write(sendBuf))
	common.Must2(io.ReadFull(conn, recvBuf[:]))
	if !bytes.Equal(sendBuf, recvBuf[:]) {
		fmt.Println(sendBuf)
		fmt.Println(recvBuf[:])
		t.Fail()
	}
	conn1.Close()
	conn2.Close()
	packet1.Close()
	packet2.Close()
	conn.Close()
	c.Close()
	s.Close()
	cancel()
}

func TestTrojanConcurrentConnections(t *testing.T) {
	const numGoroutines = 20

	port := common.PickPort("tcp", "127.0.0.1")
	transportConfig := &transport.Config{
		LocalHost:  "127.0.0.1",
		LocalPort:  port,
		RemoteHost: "127.0.0.1",
		RemotePort: port,
	}
	ctx := t.Context()
	ctx = config.WithConfig(ctx, transport.Name, transportConfig)
	ctx = config.WithConfig(ctx, freedom.Name, &freedom.Config{})

	tcpClient, err := transport.NewClient(ctx, nil)
	common.Must(err)
	tcpServer, err := transport.NewServer(ctx, nil)
	common.Must(err)

	serverPort := common.PickPort("tcp", "127.0.0.1")
	authConfig := &memory.Config{Passwords: []string{"password"}}
	clientConfig := &Config{
		RemoteHost: "127.0.0.1",
		RemotePort: serverPort,
	}
	serverConfig := &Config{
		LocalHost:  "127.0.0.1",
		LocalPort:  serverPort,
		RemoteHost: "127.0.0.1",
		RemotePort: util.EchoPort,
	}

	ctx = config.WithConfig(ctx, memory.Name, authConfig)
	clientCtx := config.WithConfig(ctx, Name, clientConfig)
	serverCtx := config.WithConfig(ctx, Name, serverConfig)

	c, err := NewClient(clientCtx, tcpClient)
	common.Must(err)
	defer c.Close()

	s, err := NewServer(serverCtx, tcpServer)
	common.Must(err)
	defer s.Close()

	var wg sync.WaitGroup
	var successCount atomic.Int32

	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			conn, err := c.DialConn(&tunnel.Address{
				DomainName:  "example.com",
				AddressType: tunnel.DomainName,
			}, nil)
			if err != nil {
				return
			}
			defer conn.Close()

			data := fmt.Appendf(nil, "test-data-%d", id)
			if _, err := conn.Write(data); err != nil {
				return
			}
			successCount.Add(1)
		}(i)
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond)

	t.Logf("Concurrent connection test: %d successful", successCount.Load())
}

func TestTrojanQuickClose(t *testing.T) {
	port := common.PickPort("tcp", "127.0.0.1")
	transportConfig := &transport.Config{
		LocalHost:  "127.0.0.1",
		LocalPort:  port,
		RemoteHost: "127.0.0.1",
		RemotePort: port,
	}
	ctx := t.Context()
	ctx = config.WithConfig(ctx, transport.Name, transportConfig)
	ctx = config.WithConfig(ctx, freedom.Name, &freedom.Config{})

	tcpClient, err := transport.NewClient(ctx, nil)
	common.Must(err)
	tcpServer, err := transport.NewServer(ctx, nil)
	common.Must(err)

	serverPort := common.PickPort("tcp", "127.0.0.1")
	authConfig := &memory.Config{Passwords: []string{"password"}}
	clientConfig := &Config{
		RemoteHost: "127.0.0.1",
		RemotePort: serverPort,
	}
	serverConfig := &Config{
		LocalHost:  "127.0.0.1",
		LocalPort:  serverPort,
		RemoteHost: "127.0.0.1",
		RemotePort: util.EchoPort,
	}

	ctx = config.WithConfig(ctx, memory.Name, authConfig)
	clientCtx := config.WithConfig(ctx, Name, clientConfig)
	serverCtx := config.WithConfig(ctx, Name, serverConfig)

	c, err := NewClient(clientCtx, tcpClient)
	common.Must(err)
	defer c.Close()

	s, err := NewServer(serverCtx, tcpServer)
	common.Must(err)
	defer s.Close()

	conn, err := c.DialConn(&tunnel.Address{
		DomainName:  "example.com",
		AddressType: tunnel.DomainName,
	}, nil)
	if err != nil {
		t.Fatalf("DialConn failed: %v", err)
	}
	// Close immediately after creating
	if err := conn.Close(); err != nil {
		t.Fatalf("Quick close failed: %v", err)
	}
	t.Log("Quick close test passed")
}

func TestTrojanMultipleClose(t *testing.T) {
	port := common.PickPort("tcp", "127.0.0.1")
	transportConfig := &transport.Config{
		LocalHost:  "127.0.0.1",
		LocalPort:  port,
		RemoteHost: "127.0.0.1",
		RemotePort: port,
	}
	ctx := t.Context()
	ctx = config.WithConfig(ctx, transport.Name, transportConfig)
	ctx = config.WithConfig(ctx, freedom.Name, &freedom.Config{})

	tcpClient, err := transport.NewClient(ctx, nil)
	common.Must(err)

	serverPort := common.PickPort("tcp", "127.0.0.1")
	authConfig := &memory.Config{Passwords: []string{"password"}}
	clientConfig := &Config{
		RemoteHost: "127.0.0.1",
		RemotePort: serverPort,
	}

	ctx = config.WithConfig(ctx, memory.Name, authConfig)
	clientCtx := config.WithConfig(ctx, Name, clientConfig)

	c, err := NewClient(clientCtx, tcpClient)
	common.Must(err)
	defer c.Close()

	// Test multiple Close() calls don't panic
	for range 3 {
		c.Close()
	}
	t.Log("Multiple close test passed")
}
