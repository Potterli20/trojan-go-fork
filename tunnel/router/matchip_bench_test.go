package router

import (
	"net"
	"net/netip"
	"testing"

	v2geodata "github.com/xtls/xray-core/common/geodata"
)

func toPrefixes(t testing.TB, list []*v2geodata.CIDR) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(list))
	for _, c := range list {
		p, ok := prefixFromCIDR(c)
		if !ok {
			continue
		}
		out = append(out, p)
	}
	return out
}

// 新旧实现必须逐项等价：v4/v6、前缀边界（0/32/128）、v4-mapped、以及
// 条目地址带主机位（旧实现用 Mask 归零，新实现必须 Masked）。
func TestMatchIPPrefixesEquivalence(t *testing.T) {
	cases := []struct {
		name  string
		cidrs []*v2geodata.CIDR
	}{
		{"v4 0", []*v2geodata.CIDR{{Ip: net.IPv4(0, 0, 0, 0).To4(), Prefix: 0}}},
		{"v4 24", []*v2geodata.CIDR{{Ip: net.IPv4(1, 2, 3, 0).To4(), Prefix: 24}}},
		{"v4 24 host bits", []*v2geodata.CIDR{{Ip: net.IPv4(1, 2, 3, 99).To4(), Prefix: 24}}},
		{"v4 32", []*v2geodata.CIDR{{Ip: net.IPv4(1, 2, 3, 4).To4(), Prefix: 32}}},
		{"v6 0", []*v2geodata.CIDR{{Ip: net.ParseIP("::"), Prefix: 0}}},
		{"v6 32", []*v2geodata.CIDR{{Ip: net.ParseIP("2001:db8::"), Prefix: 32}}},
		{"v6 128", []*v2geodata.CIDR{{Ip: net.ParseIP("2001:db8::1"), Prefix: 128}}},
		{"v4-mapped 24", []*v2geodata.CIDR{{Ip: net.ParseIP("::ffff:1.2.3.0"), Prefix: 24}}},
		{"mixed", []*v2geodata.CIDR{
			{Ip: net.IPv4(1, 2, 3, 0).To4(), Prefix: 24},
			{Ip: net.ParseIP("2001:db8::"), Prefix: 32},
		}},
	}
	targets := []net.IP{
		net.ParseIP("1.2.3.4"),
		net.ParseIP("1.2.3.4").To4(),
		net.ParseIP("1.2.4.4"),
		net.ParseIP("8.8.8.8").To4(),
		net.ParseIP("::ffff:1.2.3.4"),
		net.ParseIP("2001:db8::1"),
		net.ParseIP("2001:db9::1"),
	}
	for _, tc := range cases {
		newList := toPrefixes(t, tc.cidrs)
		for _, target := range targets {
			old := matchIPSlow(tc.cidrs, target)
			got := matchIPPrefixes(newList, target)
			if old != got {
				t.Fatalf("%s / target %v: 旧=%v 新=%v（实现不等价）", tc.name, target, old, got)
			}
		}
	}
}

func benchCIDRs() []*v2geodata.CIDR {
	list := make([]*v2geodata.CIDR, 0, 1024)
	for i := 0; i < 1000; i++ {
		list = append(list, &v2geodata.CIDR{
			Ip:     net.IPv4(10, byte(i>>8), byte(i), 0).To4(),
			Prefix: 24,
		})
	}
	for i := 0; i < 24; i++ {
		list = append(list, &v2geodata.CIDR{
			Ip:     net.ParseIP("2001:db8::"),
			Prefix: uint32(32 + i),
		})
	}
	return list
}

// 旧实现（生产路径改造前）：每个 CIDR 每包一次 net.CIDRMask + 一次 Mask，
// 各一次分配；目标在表里不存在时要付满整张表的代价。
func BenchmarkMatchIPSlow(b *testing.B) {
	list := benchCIDRs()
	target := net.ParseIP("203.0.113.7").To4()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		matchIPSlow(list, target)
	}
}

func BenchmarkMatchIPPrefixes(b *testing.B) {
	list := toPrefixes(b, benchCIDRs())
	target := net.ParseIP("203.0.113.7").To4()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		matchIPPrefixes(list, target)
	}
}