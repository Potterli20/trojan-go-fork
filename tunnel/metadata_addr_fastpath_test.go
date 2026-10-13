package tunnel

import (
	"bytes"
	"net"
	"testing"
)

func serialize(t *testing.T, a *Address) string {
	t.Helper()
	var buf bytes.Buffer
	if _, err := a.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo(%q): %v", a.String(), err)
	}
	return buf.String()
}

// TestNewAddressFromNetAddrMatchesStringPath 是这条快路径的等价性判据：
// 组装出来的 Address 在 String() 与线上格式（ATYP + 地址 + 端口）上必须和
// 旧的 "String() → SplitHostPort → ParseIP" 路径逐字节一致。
// 底层交出来的 IP 可能是 4 字节或 16 字节表示，而 WriteTo 走的是
// a.IP.To4()/To16()，所以两种表示必须都核对。
func TestNewAddressFromNetAddrMatchesStringPath(t *testing.T) {
	v4InV6 := net.ParseIP("203.0.113.9") // 16 字节形式
	v4Short := v4InV6.To4()              // 4 字节形式
	if len(v4Short) != 4 {
		t.Fatalf("前置条件不成立：To4() 长度 %d", len(v4Short))
	}
	cases := []struct {
		network string
		addr    net.Addr
	}{
		{"udp", &net.UDPAddr{IP: v4InV6, Port: 44567}},
		{"udp", &net.UDPAddr{IP: v4Short, Port: 44567}},
		{"udp", &net.UDPAddr{IP: net.ParseIP("2001:db8::1"), Port: 53}},
		{"udp", &net.UDPAddr{IP: net.ParseIP("::ffff:192.0.2.1"), Port: 9}},
		{"tcp", &net.TCPAddr{IP: v4Short, Port: 8080}},
		{"tcp", &net.TCPAddr{IP: net.ParseIP("2001:db8::1"), Port: 8080}},
	}
	for _, tc := range cases {
		fast, err := NewAddressFromNetAddr(tc.network, tc.addr)
		if err != nil {
			t.Fatalf("fast path %v: %v", tc.addr, err)
		}
		slow, err := NewAddressFromAddr(tc.network, tc.addr.String())
		if err != nil {
			t.Fatalf("slow path %v: %v", tc.addr, err)
		}
		if fast.String() != slow.String() {
			t.Errorf("%v: String 不一致 fast=%q slow=%q", tc.addr, fast.String(), slow.String())
		}
		if got, want := serialize(t, fast), serialize(t, slow); got != want {
			t.Errorf("%v: 线上格式不一致 fast=% x slow=% x", tc.addr, got, want)
		}
		if fast.NetworkType != slow.NetworkType {
			t.Errorf("%v: NetworkType 不一致", tc.addr)
		}
	}
}

// TestNewAddressFromNetAddrFallsBack 保证「不是 UDP/TCP 地址」时行为完全不变：
// 中继的写方向拿到的常常是 *tunnel.Address（可能是域名），这类必须退回旧解析路径。
func TestNewAddressFromNetAddrFallsBack(t *testing.T) {
	domain := &Address{
		DomainName:  "example.com",
		Port:        443,
		NetworkType: "udp",
		AddressType: DomainName,
	}
	fast, err := NewAddressFromNetAddr("udp", domain)
	if err != nil {
		t.Fatalf("fallback: %v", err)
	}
	if fast.AddressType != DomainName || fast.DomainName != "example.com" {
		t.Fatalf("域名地址没走回解析路径: %+v", fast)
	}
	if serialize(t, fast) != serialize(t, domain) {
		t.Error("域名地址的线上格式与旧路径不一致")
	}

	// 空 IP 的退化情况也要与旧路径一致
	empty, err := NewAddressFromNetAddr("udp", &net.UDPAddr{Port: 1234})
	if err != nil {
		t.Fatalf("empty ip: %v", err)
	}
	slow, err := NewAddressFromAddr("udp", (&net.UDPAddr{Port: 1234}).String())
	if err != nil {
		t.Fatalf("empty ip slow: %v", err)
	}
	if empty.String() != slow.String() {
		t.Errorf("空 IP 退化行为变了: fast=%q slow=%q", empty.String(), slow.String())
	}
}

// TestNewAddressFromNetAddrZoneBecomesIPv6 记录唯一的、有意为之的行为差异：
// 带 zone 的链路本地地址。旧路径的 net.ParseIP 不认 zone，于是把它整个当成
// **域名**存进 Address（ATYP=3），下游按域名匹配的路由规则和反查解析都跟着歪。
// 快路径直接按 IPv6 处理。
func TestNewAddressFromNetAddrZoneBecomesIPv6(t *testing.T) {
	addr := &net.UDPAddr{IP: net.ParseIP("fe80::1"), Zone: "eth0", Port: 5353}
	fast, err := NewAddressFromNetAddr("udp", addr)
	if err != nil {
		t.Fatalf("zone case: %v", err)
	}
	if fast.AddressType != IPv6 {
		t.Fatalf("带 zone 的 IPv6 应当判为 IPv6，实际 %v", fast.AddressType)
	}
	if fast.DomainName != "" {
		t.Fatalf("不该带域名: %q", fast.DomainName)
	}
	// 对照：旧路径确实把它当成了域名（这正是快路径要避开的）
	slow, err := NewAddressFromAddr("udp", addr.String())
	if err != nil {
		t.Fatalf("zone case slow: %v", err)
	}
	if slow.AddressType != DomainName {
		t.Skipf("对照不成立（stdlib 现在能解析带 zone 的地址了）: %v", slow.AddressType)
	}
}
