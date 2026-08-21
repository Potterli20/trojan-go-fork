//go:build linux

package freedom

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/test/util"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

func getSocketMark(c syscall.Conn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var mark int
	var getErr error
	err = raw.Control(func(fd uintptr) {
		mark, getErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK)
	})
	if err != nil {
		return 0, err
	}
	return mark, getErr
}

func requireCapNetAdmin(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		return
	}
	out, err := exec.Command("capsh", "--print").CombinedOutput()
	if err != nil {
		t.Skipf("SO_MARK requires root or CAP_NET_ADMIN; skipping (capsh err: %v)", err)
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Current:") && strings.Contains(line, "cap_net_admin") {
			return
		}
	}
	t.Skip("SO_MARK requires root or CAP_NET_ADMIN; skipping")
}

func TestFwmark_NewClient_ConfigParsed(t *testing.T) {
	ctx := newCtxWithConfig(&Config{OutboundFwmark: 0x42})
	c, err := NewClient(ctx, nil)
	common.Must(err)
	defer c.Close()
	if c.outboundFwmark != 0x42 {
		t.Fatalf("expected outboundFwmark=0x42, got %#x", c.outboundFwmark)
	}
}

func TestFwmark_NewClient_ZeroIsNoOp(t *testing.T) {
	ctx := newCtxWithConfig(&Config{OutboundFwmark: 0})
	c, err := NewClient(ctx, nil)
	common.Must(err)
	defer c.Close()
	if c.outboundFwmark != 0 {
		t.Fatalf("expected outboundFwmark=0, got %#x", c.outboundFwmark)
	}
	if ctrl := fwmarkControl(c.outboundFwmark); ctrl != nil {
		t.Fatal("fwmarkControl(0) should return nil")
	}
}

func TestFwmark_DialConn_SetsSockOpt(t *testing.T) {
	requireCapNetAdmin(t)
	const mark = 0x1234

	SetGlobalOutbound(nil, mark)
	defer resetGlobalOutbound()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &Client{ctx: ctx, cancel: cancel}
	addr, err := tunnel.NewAddressFromAddr("tcp", util.EchoAddr)
	common.Must(err)

	conn, err := client.DialConn(addr, nil)
	common.Must(err)
	defer conn.Close()

	tcpConn := conn.(*Conn).Conn.(*net.TCPConn)
	got, err := getSocketMark(tcpConn)
	common.Must(err)
	if got != mark {
		t.Fatalf("expected SO_MARK=%#x, got %#x", mark, got)
	}

	payload := util.GeneratePayload(256)
	common.Must2(conn.Write(payload))
	recv := make([]byte, 256)
	common.Must2(conn.Read(recv))
	if !bytes.Equal(payload, recv) {
		t.Fatal("echo mismatch")
	}
}

func TestFwmark_DialPacket_SetsSockOpt(t *testing.T) {
	requireCapNetAdmin(t)
	const mark = 0x5678

	SetGlobalOutbound(nil, mark)
	defer resetGlobalOutbound()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &Client{ctx: ctx, cancel: cancel}
	pc, err := client.DialPacket(nil)
	common.Must(err)
	defer pc.Close()

	udpConn := pc.(*PacketConn).UDPConn
	got, err := getSocketMark(udpConn)
	common.Must(err)
	if got != mark {
		t.Fatalf("expected SO_MARK=%#x, got %#x", mark, got)
	}
}

func TestFwmark_CombinedWithLocalAddr(t *testing.T) {
	requireCapNetAdmin(t)
	const mark = 0xabcd
	const localIP = "127.0.0.2"

	SetGlobalOutbound(net.ParseIP(localIP), mark)
	defer resetGlobalOutbound()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &Client{ctx: ctx, cancel: cancel}
	addr, err := tunnel.NewAddressFromAddr("tcp", util.EchoAddr)
	common.Must(err)

	conn, err := client.DialConn(addr, nil)
	common.Must(err)
	defer conn.Close()

	tcpConn := conn.(*Conn).Conn.(*net.TCPConn)
	if !tcpConn.LocalAddr().(*net.TCPAddr).IP.Equal(net.ParseIP(localIP)) {
		t.Fatalf("local IP mismatch")
	}
	got, err := getSocketMark(tcpConn)
	common.Must(err)
	if got != mark {
		t.Fatalf("expected SO_MARK=%#x, got %#x", mark, got)
	}
}

func TestFwmark_JSONRoundtrip(t *testing.T) {
	blob := []byte(`{"outbound_fwmark":256}`)
	ctx, err := config.WithJSONConfig(context.Background(), blob)
	common.Must(err)
	cfg := config.FromContext(ctx, Name).(*Config)
	if cfg.OutboundFwmark != 256 {
		t.Fatalf("expected 256, got %d", cfg.OutboundFwmark)
	}
}

func TestFwmark_YAMLRoundtrip(t *testing.T) {
	blob := fmt.Appendf(nil, "outbound-fwmark: %d\n", 0x100)
	ctx, err := config.WithYAMLConfig(context.Background(), blob)
	common.Must(err)
	cfg := config.FromContext(ctx, Name).(*Config)
	if cfg.OutboundFwmark != 0x100 {
		t.Fatalf("expected %d, got %d", 0x100, cfg.OutboundFwmark)
	}
}
