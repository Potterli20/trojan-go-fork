package tunnel

import (
	"net"
	"strings"
	"sync"
	"testing"
)

// 这组测试的前提是：一个 *Address 会被多条并发会话**共享**，这不是理论风险而是既成事实——
//   - tunnel/dokodemo/server.go 的 s.targetAddr 每实例一个，却塞进每条 TCP 会话的
//     targetMetadata（:135）和每个 UDP 会话的 fixedMetadata（:34）；forward/nat/custom
//     模式下 relay 给每条会话各起一个 goroutine。
//   - tunnel/freedom/client.go 的 c.proxyAddr 同理，被所有拨号并发使用。
//
// 已知且**尚未**修掉的一处，先记在这里免得被当成已解决：Metadata.WriteTo 仍然会写
// r.Address.NetworkType = "tcp"。它在非测试代码里没有任何读者（读者链只有
// Address.Network() ← Metadata.Network()，而后者零调用点），却因为这些共享对象构成并发写：
// 实测 16 条会话并发调 WriteTo 时 -race 直接报 DATA RACE。删掉它的语义代价为零，但
// tunnel/metadata_appendto_test.go:123 用一条断言把这个副作用明确钉住为"必须保住"，那是
// 另一条工作线上主动的语义决定，这里不越过它改动，需要人类对齐之后再动。
// 下面的判据因此只覆盖 ResolveIP 那一半。

// TestResolveIPDoesNotWriteExportedIP 钉住这次的修复点。旧实现把 DNS 结果写回导出的
// net.IP，而读 net.IP 的路径完全不加锁（Address.AppendTo 的 IPv4/IPv6 分支直接
// append(a.IP.To4()...)，String() 也读它）。域名地址本来 IP==nil，这个写回既没有读者
// （域名分支只看 DomainName，见 TestAppendToOnDomainAddressIgnoresIP），又给并发送一次
// 竞争，纯亏。现在缓存落在非导出的 resolvedIP 上，并在锁内读写。
func TestResolveIPDoesNotWriteExportedIP(t *testing.T) {
	addr := &Address{
		AddressType: DomainName,
		DomainName:  "localhost",
		Port:        80,
		NetworkType: "tcp",
	}
	ip, err := addr.ResolveIP()
	if err != nil {
		t.Skipf("本环境无法解析 localhost：%v", err)
	}
	if ip == nil || !ip.IsLoopback() {
		t.Fatalf("localhost 应解析到回环地址，实际 %v", ip)
	}
	if addr.IP != nil {
		t.Fatalf("ResolveIP 不再允许写导出的 net.IP（会与并发读 IP 的序列化路径竞争），实际 IP=%v", addr.IP)
	}
}

// TestConcurrentResolveIPIsRaceFreeAndStable 是可达性判据：完全照 dokodemo 的共享方式
// 构造——多条会话指向同一个 *Address，并发解析并同时走序列化读路径。在 -race 下这就是
// 那条竞争的现场；同时断言所有会话拿到同一个 IP，因为缓存只该落一次。
//
// 这里刻意用 AppendTo 而不是 WriteTo：AppendTo 不碰 NetworkType，能单独验证 ResolveIP
// 这一半修复；WriteTo 的那半被上面记的未决副作用挡着。
func TestConcurrentResolveIPIsRaceFreeAndStable(t *testing.T) {
	shared := &Address{
		AddressType: DomainName,
		DomainName:  "localhost",
		Port:        8080,
		NetworkType: "udp",
	}
	first, err := shared.ResolveIP()
	if err != nil {
		t.Skipf("本环境无法解析 localhost：%v", err)
	}

	const sessions = 16
	var wg sync.WaitGroup
	got := make([]net.IP, sessions)
	for i := range sessions {
		wg.Go(func() {
			for range 32 {
				ip, err := shared.ResolveIP()
				if err != nil {
					t.Errorf("ResolveIP: %v", err)
					return
				}
				got[i] = ip
				// 序列化读路径：读 DomainName、不读 IP——修好之后两边不再互相踩
				if _, err := shared.AppendTo(make([]byte, 0, 32)); err != nil {
					t.Errorf("AppendTo: %v", err)
					return
				}
			}
		})
	}
	wg.Wait()

	for i, ip := range got {
		if !ip.Equal(first) {
			t.Fatalf("会话 %d 拿到的 IP 与首次解析不一致：%v vs %v", i, ip, first)
		}
	}
	if shared.IP != nil {
		t.Fatalf("并发跑完后导出的 net.IP 竟然被写了：%v", shared.IP)
	}
}

// TestAppendToOnDomainAddressIgnoresIP 记录上面那条修复赖以成立的前提：域名地址的
// 序列化与 String() 只看 DomainName，完全不碰 net.IP。正因为没有读者，旧实现往 IP 里
// 塞 DNS 缓存才是纯粹的无效写。这条断言哪天变红，说明有人开始读域名地址的 IP 字段，
// 那 ResolveIP 的缓存策略要重新讨论，不是简单挪个字段的事。
func TestAppendToOnDomainAddressIgnoresIP(t *testing.T) {
	addr := &Address{AddressType: DomainName, DomainName: "example.com", Port: 443}
	withIP := &Address{AddressType: DomainName, DomainName: "example.com", Port: 443, IP: net.IPv4(1, 2, 3, 4)}

	a, _ := addr.AppendTo(make([]byte, 0, 32))
	b, _ := withIP.AppendTo(make([]byte, 0, 32))
	if string(a) != string(b) {
		t.Fatalf("域名地址的线上格式竟然受 net.IP 字段影响：\n %x\n %x", a, b)
	}
	if s1, s2 := addr.String(), withIP.String(); s1 != s2 || !strings.HasSuffix(s1, ":443") {
		t.Fatalf("域名地址的 String() 竟然受 net.IP 字段影响：%q vs %q", s1, s2)
	}
}
