package freedom

import (
	"strings"
	"testing"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// TestMaxSizeSocksFrameFitsPooledCapacity 钉住 SOCKS5 UDP 写出方向的池化预留。
//
// WriteWithMetadata 在借来的 scratch 上就地攒「RSV+FRAG(3) + 地址块(≤259) + 负载
// (≤MaxPacketSize)」。池里那块原本只有 MaxPacketSize：满尺寸负载一定把 append 撑到界外，
// 归还闸门按 cap 判定所以池本身没坏，但每包白多一份 ~16KB 的一次性分配——省下的分配
// 又被加了回来。口径同 tunnel/trojan/packet.go 的 packetBufCap。
//
// 判据用确定性的容量事实，不用"再 Get 一次看有没有新建对象"（见 AGENTS.md 关于
// sync.Pool 本地队列在 -race 下被窃取、身份判据会假失效的说明）。
func TestMaxSizeSocksFrameFitsPooledCapacity(t *testing.T) {
	scratch := getSocksPacketBuf()
	defer putSocksPacketBuf(scratch)
	reserved := cap(scratch)
	if reserved != socksFrameBufCap {
		t.Fatalf("池的预留 %d 小于声明的 socksFrameBufCap %d", reserved, socksFrameBufCap)
	}

	worst := &tunnel.Address{
		AddressType: tunnel.DomainName,
		DomainName:  strings.Repeat("a", 255),
		Port:        65535,
	}
	out := append(scratch[:0], 0, 0, 0) // RSV, FRAG
	var err error
	out, err = worst.AppendTo(out)
	if err != nil {
		t.Fatalf("最坏合法地址本应能序列化: %v", err)
	}
	out = append(out, make([]byte, MaxPacketSize)...)

	if cap(out) != reserved {
		t.Fatalf("最坏合法帧把预留缓冲从 %d 撑到 %d：每个满尺寸包都会新造一份缓冲", reserved, cap(out))
	}
}
