package socks

import (
	"strings"
	"testing"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// TestMaxSizeResponseFrameFitsReservedBuffer 钉住会话守护协程那块组帧缓冲的预留容量。
//
// 回包帧是「RSV+FRAG(3) + 地址块 + 负载」就地攒在 responseBuf 上，地址块的线上上限是
// 域名分支 ATYP(1)+长度字节(1)+255 域名+端口(2)=259。缓冲只留 MaxPacketSize 时，
// 满尺寸负载一定把 append 撑到界外：responseBuf 停在 8192，每包另外新造一份 ~16KB，
// 下一包再从 8192 撑一次。远端按线上格式合法发满尺寸 datagram 就能逐包加压本地 GC。
//
// 与 tunnel/trojan/packet_serialize_test.go 同口径：刻意**不**用"写完再取一次池看有没有
// 新建对象"来判（sync.Pool 本地队列会被 P 窃取，-race 下有假失效），撑没撑爆预留是纯
// 确定性的容量事实，直接钉它。
func TestMaxSizeResponseFrameFitsReservedBuffer(t *testing.T) {
	responseBuf := make([]byte, responseFrameCap)
	reserved := cap(responseBuf)

	worst := &tunnel.Address{
		AddressType: tunnel.DomainName,
		DomainName:  strings.Repeat("a", 255),
		Port:        65535,
	}
	out := responseBuf[:0]
	out = append(out, 0, 0, 0) // RSV, FRAG
	var err error
	out, err = worst.AppendTo(out)
	if err != nil {
		t.Fatalf("最坏合法地址本应能序列化: %v", err)
	}
	out = append(out, make([]byte, MaxPacketSize)...)

	if cap(out) != reserved {
		t.Fatalf("最坏合法回包帧把预留缓冲从 %d 撑到 %d：每满尺寸包都会新造一份缓冲，池化/复用形同失效",
			reserved, cap(out))
	}
	if len(out) > reserved {
		t.Fatalf("帧长 %d 超过预留 %d", len(out), reserved)
	}
}
