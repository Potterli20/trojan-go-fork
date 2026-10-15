package tunnel

import (
	"bytes"
	"net"
	"strings"
	"testing"
)

// 这组判据是为 "把地址序列化的唯一格式定义处从 WriteTo 挪到 AppendTo" 那次重构准备的。
// 关键点在于**不能只拿 WriteTo 来证明 AppendTo**——重构后 WriteTo 就是 AppendTo 的封装，
// 两者互为参照等于什么都没测。所以金标准是手写的字节数组。
//
// ATYP 取值：IPv4=1、DomainName=3、IPv6=4（见 metadata.go 的常量块）。
func TestAddressAppendToGoldenBytes(t *testing.T) {
	longDomain := strings.Repeat("a", 255)
	cases := []struct {
		name    string
		address *Address
		want    []byte
	}{
		{
			name:    "ipv4",
			address: &Address{AddressType: IPv4, IP: net.IPv4(203, 0, 113, 9), Port: 8080},
			want:    []byte{0x01, 203, 0, 113, 9, 0x1F, 0x90},
		},
		{
			name:    "ipv4 端口 0",
			address: &Address{AddressType: IPv4, IP: net.IPv4(10, 0, 0, 1), Port: 0},
			want:    []byte{0x01, 10, 0, 0, 1, 0x00, 0x00},
		},
		{
			name:    "ipv4 端口上界",
			address: &Address{AddressType: IPv4, IP: net.IPv4(10, 0, 0, 1), Port: 65535},
			want:    []byte{0x01, 10, 0, 0, 1, 0xFF, 0xFF},
		},
		{
			// 4 字节与 16 字节两种 IP 表示必须产出完全相同的 7 字节，这是旧实现的行为
			// （两者都走 w.Write(a.IP.To4())）
			name:    "ipv4 用 16 字节的 v4-mapped 表示",
			address: &Address{AddressType: IPv4, IP: net.ParseIP("::ffff:203.0.113.9"), Port: 8080},
			want:    []byte{0x01, 203, 0, 113, 9, 0x1F, 0x90},
		},
		{
			name:    "ipv6",
			address: &Address{AddressType: IPv6, IP: net.ParseIP("2001:db8::1"), Port: 443},
			want:    append([]byte{0x04}, append(net.ParseIP("2001:db8::1").To16(), 0x01, 0xBB)...),
		},
		{
			name:    "域名",
			address: &Address{AddressType: DomainName, DomainName: "example.com", Port: 53},
			want:    append([]byte{0x03, 11}, append([]byte("example.com"), 0x00, 0x35)...),
		},
		{
			name:    "域名长度上界 255",
			address: &Address{AddressType: DomainName, DomainName: longDomain, Port: 1},
			want:    append([]byte{0x03, 255}, append([]byte(longDomain), 0x00, 0x01)...),
		},
		{
			name:    "空域名（长度字节为 0）",
			address: &Address{AddressType: DomainName, DomainName: "", Port: 80},
			want:    []byte{0x03, 0x00, 0x00, 0x50},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.address.AppendTo(nil)
			if err != nil {
				t.Fatalf("AppendTo: %v", err)
			}
			if !bytes.Equal(got, c.want) {
				t.Fatalf("线上字节不符\n got %v\nwant %v", got, c.want)
			}

			// 追加语义：已有内容必须原样保留在前面，返回值只是变长
			prefix := []byte("RSV")
			appended, err := c.address.AppendTo(append([]byte(nil), prefix...))
			if err != nil {
				t.Fatalf("AppendTo(with prefix): %v", err)
			}
			if !bytes.Equal(appended[:3], prefix) || !bytes.Equal(appended[3:], c.want) {
				t.Fatalf("前缀被改写或偏移不对: %v", appended)
			}

			// 与 WriteTo 一致（WriteTo 现在就是它的封装，这条挡住将来有人再拆开）
			var buf bytes.Buffer
			if _, err := c.address.WriteTo(&buf); err != nil {
				t.Fatalf("WriteTo: %v", err)
			}
			if !bytes.Equal(buf.Bytes(), c.want) {
				t.Fatalf("WriteTo 与 AppendTo 输出不一致\nWriteTo %v\nAppendTo %v", buf.Bytes(), c.want)
			}
		})
	}
}

// TestMetadataAppendToGoldenBytes 钉住 CMD 前缀那一层：Metadata = 1 字节 Command + 地址块。
func TestMetadataAppendToGoldenBytes(t *testing.T) {
	md := &Metadata{
		Command: wireConnect,
		Address: &Address{AddressType: DomainName, DomainName: "example.com", Port: 53},
	}
	want := append([]byte{byte(wireConnect), 0x03, 11}, append([]byte("example.com"), 0x00, 0x35)...)

	got, err := md.AppendTo(nil)
	if err != nil {
		t.Fatalf("Metadata.AppendTo: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Metadata 线上字节不符\n got %v\nwant %v", got, want)
	}

	var buf bytes.Buffer
	n, err := md.WriteTo(&buf)
	if err != nil {
		t.Fatalf("Metadata.WriteTo: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), want) || int(n) != len(want) {
		t.Fatalf("Metadata.WriteTo 与 AppendTo 不一致: %v (n=%d)", buf.Bytes(), n)
	}
	// WriteTo 顺带把 NetworkType 归一成 "tcp" 这个既有副作用必须保住
	if md.Address.NetworkType != "tcp" {
		t.Fatalf("Metadata.WriteTo 不再设置 NetworkType=tcp，现在是 %q", md.Address.NetworkType)
	}
}

// TestUnencodableAddressWritesNothingToWriter 是这次重构**唯一的行为变化**，所以单独钉住：
// 旧 Address.WriteTo 是先 w.Write(ATYP) 再校验域名长度，于是"域名超 255"会往流里留下
// 一个孤立的 ATYP 字节才报错；ATYP 非法那条同样如此。现在要么全写、要么一个字都不写。
//
// 这个方向是修而不是改坏：拿到 error 的调用方都会放弃这个包或这条连接，但半截 ATYP
// 已经进了对端的字节流，对 trojan 这种"字节流上有状态机"的协议就是流错位。
// 注意 Metadata.WriteTo 从来不受影响（它本来就攒在内部缓冲里，失败即丢弃）。
func TestUnencodableAddressWritesNothingToWriter(t *testing.T) {
	cases := []struct {
		name    string
		address *Address
	}{
		{"域名超过 255", &Address{AddressType: DomainName, DomainName: strings.Repeat("b", 256), Port: 80}},
		{"端口超过 65535", &Address{AddressType: IPv4, IP: net.ParseIP("1.2.3.4"), Port: 65536}},
		{"端口为负", &Address{AddressType: IPv4, IP: net.ParseIP("1.2.3.4"), Port: -1}},
		{"未知 ATYP", &Address{AddressType: AddressType(2), IP: net.ParseIP("1.2.3.4"), Port: 80}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			n, err := c.address.WriteTo(&buf)
			if err == nil {
				t.Fatal("应当拒绝序列化")
			}
			if buf.Len() != 0 {
				t.Fatalf("出错时不该往 writer 里留字节，实际留下 %v（%d 字节）", buf.Bytes(), buf.Len())
			}
			if n != 0 {
				t.Fatalf("出错时返回的字节数应为 0，实际 %d", n)
			}
		})
	}
}

// TestMetadataWriteToRejectsNilAddress 记录另一处从 panic 变成 error 的地方：
// 旧实现里 Address 为 nil 会直接在 WriteTo 里空指针崩掉，而 Metadata.Network() 等
// 方法本来就允许 Address==nil，所以这个状态是可达的。
func TestMetadataWriteToRejectsNilAddress(t *testing.T) {
	md := &Metadata{Command: wireConnect}
	var buf bytes.Buffer
	if _, err := md.WriteTo(&buf); err == nil {
		t.Fatal("Address 为 nil 时应当报错，而不是 panic 或静默写出")
	}
	if buf.Len() != 0 {
		t.Fatalf("失败时不该写出任何字节，实际 %v", buf.Bytes())
	}
}

// TestAddressAppendToReusesCallerBuffer 是这次改动的性能门禁本身：
// 往一个容量足够的调用方切片上追加，一次堆分配都不该发生。旧写法只能走
// w.Write([]byte{...})，那些复合字面量都会逃逸——实测域名地址 4 次、IP 地址 2 次
// （本机 Ryzen 5 4500U，go1.27.1），而 freedom/socks 的 UDP 路径每包一次。
func TestAddressAppendToReusesCallerBuffer(t *testing.T) {
	buf := make([]byte, 0, 512)
	addresses := []*Address{
		{AddressType: IPv4, IP: net.IPv4(203, 0, 113, 9), Port: 8080},
		{AddressType: IPv6, IP: net.ParseIP("2001:db8::1"), Port: 443},
		{AddressType: DomainName, DomainName: "www.example.com", Port: 443},
	}
	for _, a := range addresses {
		t.Run(a.String(), func(t *testing.T) {
			n := testing.AllocsPerRun(2000, func() {
				out, err := a.AppendTo(buf[:0])
				if err != nil {
					t.Fatal(err)
				}
				_ = out
			})
			if n != 0 {
				t.Fatalf("%s 的 AppendTo 在容量足够的调用方切片上分配了 %v 次，期望 0", a.String(), n)
			}
		})
	}
}
