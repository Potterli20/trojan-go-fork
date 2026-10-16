package tunnel

import (
	"net"
	"net/netip"
)

// SessionKey 是 UDP 入站会话表的键类型。可比较，可直接作 map 键。
//
// 存在的理由：socks / dokodemo / tproxy 三层过去都拿 `addr.String()` 当键，那是**每个
// UDP 包**一次 IP 格式化 + 一次 map 字符串哈希。换键的前提是"哪些地址算同一条会话"
// 这件事不能变，而它由两条不那么显然的规则决定（判据见 udp_session_key_test.go）：
//
//	① v4 与 v4-mapped-v6 同键。`(*net.UDPAddr).AddrPort()` 在双栈 socket 上收到 IPv4
//	   对端时给的是 4-in-6 形态，直接拿它当键会把同一台客户端拆成两条会话，所以必须
//	   `Unmap()` 归一——这正是 String() 一直免费替我们做的事。
//	② zone 是键的一部分（`AddrPort()` 会保住它，实测 go1.27.1），带 zone 与不带 zone
//	   是两条会话，不能并。
//
// 不认识的 net.Addr 实现退回字符串键：那条路径仍然每包格式化一次，但行为与改造前逐字
// 一致，不会因为一个意外类型就把包丢掉或把会话并错。
type SessionKey struct {
	addrPort netip.AddrPort
	fallback string
}

// NewSessionKey 把 UDP 源地址归一成会话键。语义与 `addr.String()` 一致。
func NewSessionKey(addr net.Addr) SessionKey {
	if addr == nil {
		return SessionKey{fallback: ""}
	}
	if ua, ok := addr.(*net.UDPAddr); ok {
		ap := ua.AddrPort()
		return SessionKey{addrPort: netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())}
	}
	if ap, err := netip.ParseAddrPort(addr.String()); err == nil {
		return SessionKey{addrPort: netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())}
	}
	// 非 host:port 形态的 net.Addr（今天三层入站都产不出这种，留一条不改变行为的路）
	return SessionKey{fallback: addr.String()}
}

// String 只给日志用，不在每包路径上。
func (k SessionKey) String() string {
	if k.addrPort.IsValid() {
		return k.addrPort.String()
	}
	return k.fallback
}

// IsValid 报告这个键是否来自一个可解析的地址。
func (k SessionKey) IsValid() bool {
	return k.addrPort.IsValid() || k.fallback != ""
}
