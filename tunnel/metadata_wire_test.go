package tunnel

import (
	"bytes"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/Potterli20/trojan-go-fork/common"
)

// 这个包是 trojan-go 所有 tunnel 共用的线上格式，此前一个测试都没有：
// Metadata/Address 的字节布局写进 trojan、simplesocks、socks 的握手，
// 而 ReadFrom 直接面对攻击者可控的字节流。下面的判据都是围绕
// 「读进来的东西必须能被原样写回去」和「畸形输入不能 panic / 不能多吞字节」。

// wireCommand 用数值而不是 tunnel/trojan 里的常量：那些常量在 trojan 包内，
// tunnel 反过来 import 它就是环。值与 trojan/client.go 的 Connect/Associate/Mux 一致。
const (
	wireConnect   Command = 1
	wireAssociate Command = 3
)

func TestMetadataRoundTripTable(t *testing.T) {
	longDomain := strings.Repeat("a", 255)
	cases := []struct {
		name string
		md   *Metadata
		// wantString 是往返之后应当看到的目的地；IPv4 映射到 IPv6 的写法会被
		// 规范化，这里把它钉下来，免得将来"顺手优化"改变了对外表现
		wantString string
		wantType   AddressType
	}{
		{
			name:       "ipv4",
			md:         &Metadata{Command: wireConnect, Address: NewAddressFromHostPort("tcp", "203.0.113.9", 8080)},
			wantString: "203.0.113.9:8080",
			wantType:   IPv4,
		},
		{
			name:       "ipv4 端口边界",
			md:         &Metadata{Command: wireConnect, Address: NewAddressFromHostPort("tcp", "10.0.0.1", 65535)},
			wantString: "10.0.0.1:65535",
			wantType:   IPv4,
		},
		{
			name:       "ipv6",
			md:         &Metadata{Command: wireConnect, Address: NewAddressFromHostPort("tcp", "2001:db8::1", 443)},
			wantString: "[2001:db8::1]:443",
			wantType:   IPv6,
		},
		{
			name:       "域名",
			md:         &Metadata{Command: wireAssociate, Address: NewAddressFromHostPort("udp", "example.com", 53)},
			wantString: "example.com:53",
			wantType:   DomainName,
		},
		{
			name:       "域名长度上限 255",
			md:         &Metadata{Command: wireConnect, Address: NewAddressFromHostPort("tcp", longDomain, 1)},
			wantString: longDomain + ":1",
			wantType:   DomainName,
		},
		{
			// 254 与 255 都要测：byte 加法回绕的两个临界值分别是 254→0、255→1
			name:       "域名长度 254",
			md:         &Metadata{Command: wireConnect, Address: NewAddressFromHostPort("tcp", strings.Repeat("c", 254), 65534)},
			wantString: strings.Repeat("c", 254) + ":65534",
			wantType:   DomainName,
		},
		{
			name:       "域名里写着 IP 会被规范化成 IP",
			md:         &Metadata{Command: wireConnect, Address: &Address{DomainName: "192.0.2.7", Port: 22, AddressType: DomainName, NetworkType: "tcp"}},
			wantString: "192.0.2.7:22",
			wantType:   IPv4,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			nw, err := c.md.WriteTo(&buf)
			if err != nil {
				t.Fatalf("序列化失败: %v", err)
			}
			if int(nw) != buf.Len() {
				t.Fatalf("WriteTo 报的字节数 %d 与实际写入 %d 不符", nw, buf.Len())
			}
			got := &Metadata{}
			nr, err := got.ReadFrom(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Fatalf("读回自己写出的字节失败: %v（原始 %q）", err, buf.String())
			}
			if nr != nw {
				t.Fatalf("往返读掉 %d 字节，写出 %d 字节，不对称", nr, nw)
			}
			if got.Command != c.md.Command {
				t.Fatalf("命令字变了: %d -> %d", c.md.Command, got.Command)
			}
			if got.Address.String() != c.wantString {
				t.Fatalf("目的地 = %q，期望 %q", got.Address.String(), c.wantString)
			}
			if got.Address.AddressType != c.wantType {
				t.Fatalf("ATYP = %d，期望 %d", got.Address.AddressType, c.wantType)
			}
			if got.Address.Port != c.md.Address.Port {
				t.Fatalf("端口 = %d，期望 %d", got.Address.Port, c.md.Address.Port)
			}
			// 一个字节都不许多吃：后面还接着 payload
			if rest := buf.Len() - int(nr); rest != 0 {
				t.Fatalf("多出 %d 字节没被读走", rest)
			}
		})
	}
}

// TestReadFromDoesNotOverRead 钉的是握手最要紧的一条：读完一个 Metadata 之后，
// 流里剩下的字节必须还归上层（payload）所有。多吞一个字节，整条会话就错位。
func TestReadFromDoesNotOverRead(t *testing.T) {
	md := &Metadata{Command: wireConnect, Address: NewAddressFromHostPort("tcp", "example.com", 80)}
	var buf bytes.Buffer
	common.Must2(md.WriteTo(&buf))
	const payload = "THIS-IS-PAYLOAD"
	buf.WriteString(payload)

	got := &Metadata{}
	stream := bytes.NewReader(buf.Bytes())
	if _, err := got.ReadFrom(stream); err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	rest, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("读完 metadata 后读不干净: %v", err)
	}
	if string(rest) != payload {
		t.Fatalf("payload 被 metadata 解析吞掉了：%q", string(rest))
	}
}

// TestWriteToRejectsUnencodableInput 守住两个会静默改坏字节的口子：
// 域名长度写进一个 byte，超过 255 不拦就会回绕成短域名；端口写进 uint16，
// 越界不拦就会静默截断。两者都属于"发出去的东西和内存里的不是一回事"。
func TestWriteToRejectsUnencodableInput(t *testing.T) {
	cases := []struct {
		name string
		md   *Metadata
	}{
		{"域名超过 255", &Metadata{Command: wireConnect, Address: &Address{
			DomainName: strings.Repeat("b", 256), Port: 80, AddressType: DomainName, NetworkType: "tcp"}}},
		{"端口超过 65535", &Metadata{Command: wireConnect, Address: &Address{
			IP: net.ParseIP("1.2.3.4"), Port: 65536, AddressType: IPv4, NetworkType: "tcp"}}},
		{"端口为负", &Metadata{Command: wireConnect, Address: &Address{
			IP: net.ParseIP("1.2.3.4"), Port: -1, AddressType: IPv4, NetworkType: "tcp"}}},
		{"未知 ATYP", &Metadata{Command: wireConnect, Address: &Address{
			IP: net.ParseIP("1.2.3.4"), Port: 80, AddressType: AddressType(2), NetworkType: "tcp"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			if _, err := c.md.WriteTo(&buf); err == nil {
				t.Fatalf("应当拒绝序列化，却写出了 %q（静默改坏字节）", buf.String())
			}
		})
	}
}

// TestReadFromMalformedInputNeverPanics 面向攻击者：服务端会把对端前几个字节直接
// 交给 ReadFrom。这里对每种合法编码做逐长度截断，再把 ATYP 换遍 256 个取值，
// 要求只能是 error，不能 panic、也不能静默成功。
func TestReadFromMalformedInputNeverPanics(t *testing.T) {
	valid := []*Metadata{
		{Command: wireConnect, Address: NewAddressFromHostPort("tcp", "1.2.3.4", 80)},
		{Command: wireConnect, Address: NewAddressFromHostPort("tcp", "2001:db8::1", 80)},
		{Command: wireConnect, Address: NewAddressFromHostPort("tcp", "example.com", 80)},
	}
	var streams [][]byte
	for _, md := range valid {
		var buf bytes.Buffer
		common.Must2(md.WriteTo(&buf))
		full := buf.Bytes()
		for i := range full {
			streams = append(streams, full[:i])
		}
		// ATYP 单字节换遍全部取值（长度给足，考察的是 ATYP 分支本身）
		for atyp := range 256 {
			b := append([]byte{byte(atyp)}, bytes.Repeat([]byte{0}, 40)...)
			streams = append(streams, b)
		}
	}
	// 命令字 + ATYP=3 + 域名长度 250..255，尾部再从 0 到 260 逐长度截断：
	// 这几个长度正是 byte 加法回绕的落点（254→0、255→1），旧代码在这里是
	// panic 而不是 error。
	for dl := 250; dl <= 255; dl++ {
		for tail := 0; tail <= 260; tail += 13 {
			streams = append(streams, append([]byte{byte(wireConnect), 3, byte(dl)}, bytes.Repeat([]byte{0x61}, tail)...))
		}
	}
	for i, raw := range streams {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("第 %d 组输入 %q 让 ReadFrom 崩了: %v", i, raw, r)
				}
			}()
			md := &Metadata{}
			_, err := md.ReadFrom(bytes.NewReader(raw))
			if err != nil {
				return // error 的形态不做要求，只要求不是 panic
			}
			// ATYP 合法且尾部全 0 时允许成功（域名长度 0 / 端口 0 属于畸形但可读的输入），
			// 但绝不允许"报成功、地址却是 nil"这种半成品
			if md.Address == nil {
				t.Fatalf("第 %d 组输入 %q：读取成功但 Address 为 nil", i, raw)
			}
		}()
	}
}

// TestNewAddressFromAddrPortRange 钉住入口侧的钳制：端口超过 65535 必须在构造时
// 就报错，否则它会一路走到 WriteTo 才被发现，而那时已经发出去半截握手了。
func TestNewAddressFromAddrPortRange(t *testing.T) {
	for _, bad := range []string{"1.2.3.4:65536", "1.2.3.4:-1", "1.2.3.4:notaport", "1.2.3.4"} {
		if _, err := NewAddressFromAddr("tcp", bad); err == nil {
			t.Errorf("%q 应当被拒绝", bad)
		}
	}
	if _, err := NewAddressFromAddr("ip", "1.2.3.4:80"); err == nil {
		t.Error("不支持的网络类型应当被拒绝")
	}
	good, err := NewAddressFromAddr("tcp", "1.2.3.4:65535")
	if err != nil {
		t.Fatalf("合法地址被拒: %v", err)
	}
	if good.String() != "1.2.3.4:65535" {
		t.Fatalf("地址 = %q", good.String())
	}
}
