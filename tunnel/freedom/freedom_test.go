package freedom

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/Potterli20/socks5-fork"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/test/util"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

func TestConn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &Client{
		ctx:    ctx,
		cancel: cancel,
	}
	addr, err := tunnel.NewAddressFromAddr("tcp", util.EchoAddr)
	common.Must(err)
	conn1, err := client.DialConn(addr, nil)
	common.Must(err)

	sendBuf := util.GeneratePayload(1024)
	recvBuf := [1024]byte{}

	common.Must2(conn1.Write(sendBuf))
	common.Must2(conn1.Read(recvBuf[:]))

	if !bytes.Equal(sendBuf, recvBuf[:]) {
		t.Fail()
	}
	client.Close()
}

func TestPacket(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &Client{
		ctx:    ctx,
		cancel: cancel,
	}
	addr, err := tunnel.NewAddressFromAddr("udp", util.UDPEchoAddr)
	common.Must(err)
	conn1, err := client.DialPacket(nil)
	common.Must(err)

	sendBuf := util.GeneratePayload(1024)
	recvBuf := [1024]byte{}

	common.Must2(conn1.WriteTo(sendBuf, addr))
	_, _, err = conn1.ReadFrom(recvBuf[:])
	common.Must(err)

	if !bytes.Equal(sendBuf, recvBuf[:]) {
		t.Fail()
	}
	client.Close()
}

func TestSocks(t *testing.T) {
	// 暂时跳过这个测试，因为 socks5 服务器启动有问题
	t.Skip("Skipping TestSocks due to socks5 server startup issues")
	ctx, cancel := context.WithCancel(context.Background())

	socksAddr := tunnel.NewAddressFromHostPort("tcp", "127.0.0.1", common.PickPort("tcp", "127.0.0.1"))
	client := &Client{
		ctx:          ctx,
		cancel:       cancel,
		proxyAddr:    socksAddr,
		forwardProxy: true,
		noDelay:      true,
	}
	target, err := tunnel.NewAddressFromAddr("tcp", util.EchoAddr)
	common.Must(err)
	s, _ := socks5.NewClassicServer(socksAddr.String(), "127.0.0.1", "", "", 0, 0)
	s.Handle = &socks5.DefaultHandle{}
	go func() {
		s.ListenAndServe(s.Handle)
	}()

	time.Sleep(time.Second * 2)
	conn, err := client.DialConn(target, nil)
	common.Must(err)
	payload := util.GeneratePayload(1024)
	common.Must2(conn.Write(payload))

	recvBuf := [1024]byte{}
	conn.Read(recvBuf[:])
	if !bytes.Equal(recvBuf[:], payload) {
		t.Fail()
	}
	conn.Close()
	client.Close()
}
