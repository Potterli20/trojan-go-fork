//go:build linux

package tproxy

import (
	"net"
	"net/netip"
	"testing"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// IPv6 链路本地(fe80::/10)的 zone(scope_id)在两处丢失：
//  1. NewAddressFromNetAddr("udp", *net.UDPAddr{Zone:"eth0"}) 丢掉 Zone；
//  2. replySockKey 用 netip.AddrPort（无 zone）做键，把跨接口同地址合并成同一 socket。
//
// 判据分两半钉：透传 + 键区分。dialUDP 的 raddr 带 zone 由端到端用例另测。
func TestIPv6ZoneSurvivesAddressConversion(t *testing.T) {
	addr := &net.UDPAddr{
		IP:   net.ParseIP("fe80::1"),
		Port: 443,
		Zone: "eth0",
	}
	a, err := tunnel.NewAddressFromNetAddr("udp", addr)
	if err != nil {
		t.Fatalf("NewAddressFromNetAddr: %v", err)
	}
	if a.Zone != "eth0" {
		t.Fatalf("Zone 在 *net.UDPAddr -> tunnel.Address 时丢失：got %q, want %q", a.Zone, "eth0")
	}
}

func TestReplySockKeyDistinguishesZone(t *testing.T) {
	// 同一 fe80::1:443，不同 zone 必须是不同的回包 socket 键，否则跨接口
	// 的回包会串到错误接口（SO_BINDTODEVICE/ScopeId 绑错）。
	mk := func(zone string) *tunnel.Metadata {
		return &tunnel.Metadata{Address: &tunnel.Address{
			AddressType: tunnel.IPv6,
			IP:          net.ParseIP("fe80::1"),
			Port:        443,
			Zone:        zone,
		}}
	}
	k1, ok1 := replySockKey(mk("eth0"))
	k2, ok2 := replySockKey(mk("eth1"))
	if !ok1 || !ok2 {
		t.Fatalf("链路本地地址应当能生成回包键：ok1=%v ok2=%v", ok1, ok2)
	}
	if k1 == k2 {
		t.Fatalf("不同 zone 的同一地址被合成同一个键 %v：回包会串接口", k1)
	}
	// 正向对照：同 zone 必须同键（复用不能被破坏）。
	k1b, _ := replySockKey(mk("eth0"))
	if k1b != k1 {
		t.Fatalf("同 zone 生成了不同键：%v != %v，破坏了 socket 复用", k1b, k1)
	}
	// 确保键里确实含 zone（若实现改用别的机制区分，也要保证可区分且稳定）。
	_ = netip.AddrPort{}
}
