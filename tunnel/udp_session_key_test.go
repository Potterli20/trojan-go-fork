package tunnel

import (
	"net"
	"net/netip"
	"testing"
)

// TestUDPSessionKeyNormalization 钉住"UDP 会话按源地址归属"这件事今天的实际语义。
//
// socks / dokodemo / tproxy 三层的会话表都以 `addr.String()` 为键（AGENTS.md 记着这条
// 热点：每包一次格式化）。想换成 `netip.AddrPort` 省掉那次格式化，必须先把"哪些地址
// 现在算同一条会话"钉成事实——那是行为契约，不是实现细节。这里不测那三层，只测它们
// 共同依赖的键等价关系。
func TestUDPSessionKeyNormalization(t *testing.T) {
	v4 := &net.UDPAddr{IP: net.IPv4(203, 0, 113, 7), Port: 40000}
	v4Mapped := &net.UDPAddr{IP: net.IPv4(203, 0, 113, 7).To16(), Port: 40000}

	// ① 同一个 v4 的两种字节形态，今天落到同一个键：换键之后必须仍然同键，
	//    否则同一台客户端的包会分成两条会话，各自带一份队列和一个守护协程。
	if v4.String() != v4Mapped.String() {
		t.Fatalf("v4 与 v4-mapped-v6 的键不同（%q vs %q），与既有语义不符", v4.String(), v4Mapped.String())
	}
	// netip 侧要 Unmap 才等价：不 Unmap 的话 ::ffff:203.0.113.7 与 203.0.113.7 是两个键。
	fromV4, ok := netip.AddrFromSlice(v4.IP)
	if !ok {
		t.Fatal("netip.AddrFromSlice 读不出 v4")
	}
	fromMapped, ok := netip.AddrFromSlice(v4Mapped.IP)
	if !ok {
		t.Fatal("netip.AddrFromSlice 读不出 v4-mapped-v6")
	}
	if netip.AddrPortFrom(fromV4.Unmap(), 40000) != netip.AddrPortFrom(fromMapped.Unmap(), 40000) {
		t.Fatal("Unmap 之后两种形态不同键：换键时必须 Unmap")
	}

	// ② zone 是键的一部分：今天带 zone 与不带 zone 是两条会话。
	//    实测（go1.27.1）`(*net.UDPAddr).AddrPort()` **会保住 zone**，所以直接拿它当
	//    键是安全的——这一条就是"先有判据再动"的意义：写判据之前我以为它会丢。
	v6Zone := &net.UDPAddr{IP: net.ParseIP("fe80::1"), Zone: "eth0", Port: 40001}
	v6NoZone := &net.UDPAddr{IP: net.ParseIP("fe80::1"), Port: 40001}
	if v6Zone.String() == v6NoZone.String() {
		t.Fatal("带 zone 与不带 zone 今天同键，与预期不符（判据前提）")
	}
	zonedKey := v6Zone.AddrPort()
	if zonedKey.Addr().Zone() != "eth0" {
		t.Fatalf("AddrPort() 丢掉了 zone（实测 %q）：换键时它会并掉两条会话", zonedKey.Addr().Zone())
	}
	if zonedKey == v6NoZone.AddrPort() {
		t.Fatal("AddrPort() 口径下带 zone 与不带 zone 同键：与 addr.String() 的现有语义不一致")
	}
	// netip 侧手工构造必须与 AddrPort() 给出同一个键，否则换键时 zone 的归属会漂。
	rawV6, ok := netip.AddrFromSlice(v6Zone.IP)
	if !ok {
		t.Fatal("netip.AddrFromSlice 读不出 v6")
	}
	if netip.AddrPortFrom(rawV6.WithZone("eth0"), 40001) != zonedKey {
		t.Fatalf("两条构造路径不同键：AddrPort()=%q，手工 WithZone=%q", zonedKey, rawV6.WithZone("eth0"))
	}

	// ③ 端口不同必须不同键（源端口复用是 NAT 后的常态，串了就等于把别人的流量读走）
	otherPort := &net.UDPAddr{IP: net.IPv4(203, 0, 113, 7), Port: 40002}
	if v4.String() == otherPort.String() {
		t.Fatal("同地址不同端口同键，与预期不符（判据前提）")
	}
}
