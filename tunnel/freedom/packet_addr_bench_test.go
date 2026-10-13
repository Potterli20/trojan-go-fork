package freedom

import (
	"net"
	"testing"

	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// BenchmarkPacketAddress 量 ReadWithMetadata 每收一个 UDP 包做一次的那次地址组装。
//
// 两个历史基线（本机 Ryzen 5 4500U，go1.27.1，级别设成生产默认的 InfoLevel）：
//
//	addr.String() → NewAddressFromAddr 且日志门禁失效：11759 ns/op、958 B/op、28 allocs/op
//	同上，但门禁生效（log.Debug 先判级别再清洗）    ：  411 ns/op、216 B/op、 8 allocs/op
//	直接取 *net.UDPAddr 的字段（现在用的）         ：   37 ns/op、 80 B/op、 1 allocs/op
//
// 大头从来不是解析本身，而是那条最终被丢掉的 Debug 日志——所以第一条数字的
// 修复在 log 包里，不在这里。
//
// 注意：这些数字要在空闲机器上取。同一份代码在后台跑全量套件时测到 167 ns，
// 是 CPU 抢占，不是回退。
func BenchmarkPacketAddress(b *testing.B) {
	log.SetLogLevel(log.InfoLevel)
	var addr net.Addr = &net.UDPAddr{IP: net.IPv4(203, 0, 113, 9), Port: 44567}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := tunnel.NewAddressFromNetAddr("udp", addr); err != nil {
			b.Fatal(err)
		}
	}
}
