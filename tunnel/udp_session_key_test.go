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

// weirdAddr 是一个 String() 不是 host:port 形态的 net.Addr，用来验证退回字符串键这条路。
type weirdAddr struct{ s string }

func (w weirdAddr) Network() string { return "weird" }
func (w weirdAddr) String() string  { return w.s }

// TestSessionKeyMatchesStringSemantics 把 NewSessionKey 的归一化钉成事实：它必须与
// `addr.String()` 分组完全一致，否则换键就是在改"哪些包算同一条会话"——那是 NAT 后
// 两台客户端会不会被并成一条的问题，不是性能问题。
func TestSessionKeyMatchesStringSemantics(t *testing.T) {
	cases := []struct {
		name     string
		a, b     net.Addr
		wantSame bool
	}{
		{
			name:     "v4 与 v4-mapped-v6 同键",
			a:        &net.UDPAddr{IP: net.IPv4(203, 0, 113, 7), Port: 40000},
			b:        &net.UDPAddr{IP: net.IPv4(203, 0, 113, 7).To16(), Port: 40000},
			wantSame: true,
		},
		{
			name:     "16 字节 v4 与 4 字节 v4 同键",
			a:        &net.UDPAddr{IP: net.IP{203, 0, 113, 7}, Port: 40000},
			b:        &net.UDPAddr{IP: net.IPv4(203, 0, 113, 7).To4(), Port: 40000},
			wantSame: true,
		},
		{
			name:     "端口不同即不同键",
			a:        &net.UDPAddr{IP: net.IPv4(203, 0, 113, 7), Port: 40000},
			b:        &net.UDPAddr{IP: net.IPv4(203, 0, 113, 7), Port: 40001},
			wantSame: false,
		},
		{
			name:     "zone 不同即不同键",
			a:        &net.UDPAddr{IP: net.ParseIP("fe80::1"), Zone: "eth0", Port: 40000},
			b:        &net.UDPAddr{IP: net.ParseIP("fe80::1"), Zone: "eth1", Port: 40000},
			wantSame: false,
		},
		{
			name:     "带 zone 与不带 zone 不同键",
			a:        &net.UDPAddr{IP: net.ParseIP("fe80::1"), Zone: "eth0", Port: 40000},
			b:        &net.UDPAddr{IP: net.ParseIP("fe80::1"), Port: 40000},
			wantSame: false,
		},
		{
			name:     "纯 v6 不同地址不同键",
			a:        &net.UDPAddr{IP: net.ParseIP("2001:db8::1"), Port: 40000},
			b:        &net.UDPAddr{IP: net.ParseIP("2001:db8::2"), Port: 40000},
			wantSame: false,
		},
		{
			name:     "未知 Addr 实现退回字符串键，相同串同键",
			a:        weirdAddr{s: "unix:/tmp/a.sock"},
			b:        weirdAddr{s: "unix:/tmp/a.sock"},
			wantSame: true,
		},
		{
			// 只要 String() 是 host:port，未知实现也与同地址的 UDP 源同键——
			// 与字符串口径一致；只有真的解析不出 host:port 才退回字符串键。
			name:     "未知 Addr 但 String() 是 host:port，与 UDP 源同键",
			a:        weirdAddr{s: "127.0.0.1:40000"},
			b:        &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40000},
			wantSame: true,
		},
		{
			name:     "解析不出 host:port 的未知实现不与 UDP 源并键",
			a:        weirdAddr{s: "unix:/tmp/a.sock"},
			b:        &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40000},
			wantSame: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ka, kb := NewSessionKey(tc.a), NewSessionKey(tc.b)
			// 前提：字符串口径下两者是否同键，必须先与新键一致
			strSame := tc.a.String() == tc.b.String()
			if strSame != tc.wantSame {
				t.Fatalf("用例前提不成立：addr.String() 口径下 same=%v，期望 %v", strSame, tc.wantSame)
			}
			if (ka == kb) != tc.wantSame {
				t.Fatalf("SessionKey 口径 same=%v，与 addr.String() 的 %v 不一致（%q vs %q）",
					ka == kb, tc.wantSame, ka, kb)
			}
		})
	}
}

// TestSessionKeyNilAddrIsUsable 钉住 nil 不 panic：三层入站都把 src 直接塞进会话对象，
// 任何一个上游给出 nil 地址都不该让 dispatch 循环崩掉。
func TestSessionKeyNilAddrIsUsable(t *testing.T) {
	k := NewSessionKey(nil)
	if k.IsValid() {
		t.Fatal("nil 地址不该被当成有效键")
	}
	if k != NewSessionKey(nil) {
		t.Fatal("nil 地址两次归一化结果不同：同一个 nil 源会分裂成两条会话")
	}
}
