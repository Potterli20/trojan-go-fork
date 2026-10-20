package tunnel

import (
	"bytes"
	"testing"
)

// domainWire 是一个域名地址的线上字节：ATYP + 长度 + "www.example.com" + 端口。
var domainWire = append(
	append([]byte{byte(DomainName), 15}, []byte("www.example.com")...),
	0x01, 0xbb, // 443
)

// BenchmarkAddressReadFromDomain 给解析侧留一个可比数字：这条路径是 freedom / socks /
// trojan 的 UDP 中继**每个域名目标的包**一次。
//
// 这里记一次**负结果**（go1.27.1，本机 Ryzen 5 4500U）。域名分支的草稿缓冲原本是
// `make([]byte, length+2)`，看着像"每包一次可以省掉的堆分配"，于是换成栈上的
// `var scratch [257]byte`（域名最长 255 + 端口 2，定长一定装得下）。实测反而更差：
//
//	make([]byte, length+2)   172.2 ns/op    120 B/op    6 allocs/op
//	var scratch [257]byte    242.3 ns/op    384 B/op    6 allocs/op
//
// 分配**次数**一样（6），字节数却从 120 涨到 384：`buf := scratch[:length+2]` 之后要交给
// `io.ReadFull(r, buf)`，那是接口方法调用，编译器无法证明实现不留引用，于是整个 257 字节
// 数组逃逸到堆上——比按需分配的 17 字节大一个数量级。栈数组只在"不把切片交出去"时才是
// 零分配，而这里必须交出去。所以维持原样，别再试一次。
//
// 跑法：go test -run '^$' -bench BenchmarkAddressReadFrom -benchmem ./tunnel/
func BenchmarkAddressReadFromDomain(b *testing.B) {
	r := bytes.NewReader(domainWire)
	var a Address
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		r.Reset(domainWire)
		a = Address{}
		if _, err := a.ReadFrom(r); err != nil {
			b.Fatal(err)
		}
	}
	if a.DomainName != "www.example.com" || a.Port != 443 {
		// 传指针：Address 内嵌 sync.Mutex，按值塞进接口会被 go vet 判 copylocks
		b.Fatalf("解析结果不对: %v", &a)
	}
}

// BenchmarkAddressReadFromIPv4 是对照：IPv4 分支本来就用栈上的定长数组，
// 域名分支改成同样的形态之后两者的分配数应当只差"域名字符串本身"那一次。
func BenchmarkAddressReadFromIPv4(b *testing.B) {
	wire := []byte{byte(IPv4), 203, 0, 113, 7, 0x01, 0xbb}
	r := bytes.NewReader(wire)
	var a Address
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		r.Reset(wire)
		a = Address{}
		if _, err := a.ReadFrom(r); err != nil {
			b.Fatal(err)
		}
	}
	if a.Port != 443 {
		b.Fatalf("解析结果不对: %v", &a)
	}
}
