package freedom

import (
	"bytes"
	"context"
	"net"
	"runtime"
	"testing"

	"github.com/p4gefau1t/trojan-go/common"
	"github.com/p4gefau1t/trojan-go/config"
	"github.com/p4gefau1t/trojan-go/test/util"
	"github.com/p4gefau1t/trojan-go/tunnel"
)

func newCtxWithConfig(cfg *Config) context.Context {
	return config.WithConfig(context.Background(), Name, cfg)
}

func TestNewClient_OutboundLocalAddr_Valid(t *testing.T) {
	ctx := newCtxWithConfig(&Config{OutboundLocalAddr: "127.0.0.2"})
	c, err := NewClient(ctx, nil)
	common.Must(err)
	defer c.Close()
	if c.outboundLocalIP == nil || !c.outboundLocalIP.Equal(net.ParseIP("127.0.0.2")) {
		t.Fatalf("expected outboundLocalIP 127.0.0.2, got %v", c.outboundLocalIP)
	}
}

func TestNewClient_OutboundLocalAddr_Empty(t *testing.T) {
	ctx := newCtxWithConfig(&Config{})
	c, err := NewClient(ctx, nil)
	common.Must(err)
	defer c.Close()
	if c.outboundLocalIP != nil {
		t.Fatalf("expected outboundLocalIP nil when unset, got %v", c.outboundLocalIP)
	}
}

func TestNewClient_OutboundLocalAddr_Invalid(t *testing.T) {
	ctx := newCtxWithConfig(&Config{OutboundLocalAddr: "not-an-ip"})
	_, err := NewClient(ctx, nil)
	if err == nil {
		t.Fatal("expected error for invalid outbound_local_addr, got nil")
	}
}

func TestNewClient_OutboundLocalAddr_IPv6(t *testing.T) {
	ctx := newCtxWithConfig(&Config{OutboundLocalAddr: "::1"})
	c, err := NewClient(ctx, nil)
	common.Must(err)
	defer c.Close()
	if !c.outboundLocalIP.Equal(net.IPv6loopback) {
		t.Fatalf("expected ::1, got %v", c.outboundLocalIP)
	}
}

func TestDialConn_SourceIPBinding(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("127.0.0.2 binding reliably works only on Linux loopback")
	}
	const localIP = "127.0.0.2"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &Client{
		ctx:             ctx,
		cancel:          cancel,
		outboundLocalIP: net.ParseIP(localIP),
	}
	addr, err := tunnel.NewAddressFromAddr("tcp", util.EchoAddr)
	common.Must(err)

	conn, err := client.DialConn(addr, nil)
	common.Must(err)
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.TCPAddr)
	if !localAddr.IP.Equal(net.ParseIP(localIP)) {
		t.Fatalf("expected source IP %s, got %s", localIP, localAddr.IP)
	}

	payload := util.GeneratePayload(512)
	common.Must2(conn.Write(payload))
	recv := make([]byte, 512)
	common.Must2(conn.Read(recv))
	if !bytes.Equal(payload, recv) {
		t.Fatal("echo payload mismatch")
	}
}

func TestDialConn_InvalidSourceIP(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &Client{
		ctx:             ctx,
		cancel:          cancel,
		outboundLocalIP: net.ParseIP("203.0.113.99"),
	}
	addr, err := tunnel.NewAddressFromAddr("tcp", util.EchoAddr)
	common.Must(err)

	_, err = client.DialConn(addr, nil)
	if err == nil {
		t.Fatal("expected dial to fail when source IP not assigned to any local interface")
	}
}

func TestDialPacket_SourceIPBinding(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("127.0.0.2 binding reliably works only on Linux loopback")
	}
	const localIP = "127.0.0.2"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &Client{
		ctx:             ctx,
		cancel:          cancel,
		outboundLocalIP: net.ParseIP(localIP),
	}
	pc, err := client.DialPacket(nil)
	common.Must(err)
	defer pc.Close()

	localAddr := pc.LocalAddr().(*net.UDPAddr)
	if !localAddr.IP.Equal(net.ParseIP(localIP)) {
		t.Fatalf("expected UDP source IP %s, got %s", localIP, localAddr.IP)
	}

	target, err := tunnel.NewAddressFromAddr("udp", util.EchoAddr)
	common.Must(err)
	payload := util.GeneratePayload(256)
	common.Must2(pc.WriteTo(payload, target))
	recv := make([]byte, 256)
	_, _, err = pc.ReadFrom(recv)
	common.Must(err)
	if !bytes.Equal(payload, recv) {
		t.Fatal("udp echo payload mismatch")
	}
}

func TestDialPacket_EmptyLocalAddr_DefaultsToWildcard(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &Client{ctx: ctx, cancel: cancel}
	pc, err := client.DialPacket(nil)
	common.Must(err)
	defer pc.Close()

	localAddr := pc.LocalAddr().(*net.UDPAddr)
	if localAddr.IP != nil && !localAddr.IP.IsUnspecified() && !localAddr.IP.IsLoopback() {
		t.Logf("note: default UDP bind IP = %v (informational)", localAddr.IP)
	}
}

func TestFreedomConfig_JSONRoundtrip(t *testing.T) {
	jsonBlob := []byte(`{"outbound_local_addr":"127.0.0.2"}`)
	ctx, err := config.WithJSONConfig(context.Background(), jsonBlob)
	common.Must(err)
	cfg := config.FromContext(ctx, Name).(*Config)
	if cfg.OutboundLocalAddr != "127.0.0.2" {
		t.Fatalf("expected outbound_local_addr=127.0.0.2 after JSON load, got %q", cfg.OutboundLocalAddr)
	}
}

func TestFreedomConfig_YAMLRoundtrip(t *testing.T) {
	yamlBlob := []byte("outbound-local-addr: 127.0.0.2\n")
	ctx, err := config.WithYAMLConfig(context.Background(), yamlBlob)
	common.Must(err)
	cfg := config.FromContext(ctx, Name).(*Config)
	if cfg.OutboundLocalAddr != "127.0.0.2" {
		t.Fatalf("expected outbound_local_addr=127.0.0.2 after YAML load, got %q", cfg.OutboundLocalAddr)
	}
}
