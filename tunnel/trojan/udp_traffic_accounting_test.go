package trojan

import (
	"strings"
	"testing"

	"github.com/Potterli20/trojan-go-fork/statistic/memory"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// TestUDPOutboundTrafficCountsOnlyPayload 钉住出站 UDP 的流量口径：
// PacketConn.WriteWithMetadata 把完整帧（地址块 + length(2) + CRLF(2) + payload）
// 交给 OutboundConn.Write，而后者按 len(frame) 计费。于是 UDP 用户每包都把地址
// 字节也算进 sent/限速/quota：长域名一包多计约 100+ 字节，短包高频时统计与额度
// 被显著高估，且与 TCP 路径（纯载荷计费）口径不一致。
//
// 判据发 1 字节 payload 到长域名地址，断言 sent 只增加 1（纯载荷），不依赖时钟。
// 变异对照：恢复"按整帧计费"，本用例 sent 变成 100+，立刻转红。
func TestUDPOutboundTrafficCountsOnlyPayload(t *testing.T) {
	rec := &recordConn{}
	user := &memory.User{Hash: testHeaderHash}
	oc := &OutboundConn{Conn: rec, user: user}
	oc.headerWritten.Store(true) // 跳过 Associate 头，只测每包的 UDP 帧计费
	pc := &PacketConn{Conn: oc}

	m := &tunnel.Metadata{
		Command: Associate,
		Address: &tunnel.Address{
			AddressType: tunnel.DomainName,
			DomainName:  strings.Repeat("a", 100), // 长域名放大"整帧 vs 载荷"的差异
			Port:        53,
		},
	}
	const payload = "x"
	if _, err := pc.WriteWithMetadata([]byte(payload), m); err != nil {
		t.Fatalf("WriteWithMetadata: %v", err)
	}

	sent, _ := user.GetTraffic()
	if sent != uint64(len(payload)) {
		t.Fatalf("用户上行流量 = %d，期望 %d（只算 UDP 载荷，不含地址块与 length/CRLF）", sent, len(payload))
	}
	if got := oc.sent.Load(); got != uint64(len(payload)) {
		t.Fatalf("tracker sent = %d，期望 %d", got, len(payload))
	}
}

// TestUDPFirstFrameTrafficCountsOnlyPayload 覆盖首包分支：UDP 的第一个 datagram 与
// Associate 头合成一次 Write 写出（writeWithAccounting 的 written==true 分支），
// 这时同样只能按 payload 计费，不能把 Associate 头 + 地址块 + length/CRLF 全算进去。
func TestUDPFirstFrameTrafficCountsOnlyPayload(t *testing.T) {
	rec := &recordConn{}
	user := &memory.User{Hash: testHeaderHash}
	oc := &OutboundConn{Conn: rec, user: user, metadata: &tunnel.Metadata{
		Command: Associate,
		Address: &tunnel.Address{DomainName: "UDP_CONN", AddressType: tunnel.DomainName},
	}}
	// 不设 headerWritten：第一包走 WriteHeader 把 Associate 头与首帧合成一次写。
	pc := &PacketConn{Conn: oc}

	m := &tunnel.Metadata{
		Command: Associate,
		Address: &tunnel.Address{AddressType: tunnel.IPv4, IP: []byte{1, 2, 3, 4}, Port: 53},
	}
	const payload = "x"
	if _, err := pc.WriteWithMetadata([]byte(payload), m); err != nil {
		t.Fatalf("WriteWithMetadata: %v", err)
	}

	sent, _ := user.GetTraffic()
	if sent != uint64(len(payload)) {
		t.Fatalf("首包上行流量 = %d，期望 %d（不含 Associate 头与 UDP 帧头）", sent, len(payload))
	}
	// 字节内容仍必须是完整的 Associate 头 + 首帧（计数改动不许改变线上格式）。
	writes := rec.written()
	if len(writes) != 1 {
		t.Fatalf("落线帧数 = %d，期望 1（头与首负载合成一次写）", len(writes))
	}
	if !strings.HasPrefix(string(writes[0]), testHeaderHash+"\r\n") {
		t.Fatalf("首帧缺少 Associate 头: %q", writes[0][:min(16, len(writes[0]))])
	}
	if !strings.HasSuffix(string(writes[0]), payload) {
		t.Fatalf("首帧负载不对: %q", writes[0][max(0, len(writes[0])-4):])
	}
}
