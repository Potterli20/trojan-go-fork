package trojan

import (
	"testing"

	"github.com/Potterli20/trojan-go-fork/statistic/memory"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// TestFirstPayloadIsCountedInTraffic 钉住出站流量的首个负载：它跟帧头合成一次
// Write 发出（见 WriteHeader 的注释），旧实现直接在 written 分支 return len(p)，
// 既不计用户流量/限速也不计 tracker 的 sent——每条连接少记一块 relay buffer 的量。
func TestFirstPayloadIsCountedInTraffic(t *testing.T) {
	c, _ := newHeaderConn(&tunnel.Metadata{
		Command: Connect,
		Address: &tunnel.Address{AddressType: tunnel.IPv4, IP: []byte{127, 0, 0, 1}, Port: 443},
	})
	user, ok := c.user.(*memory.User)
	if !ok {
		t.Fatalf("测试夹具的 user 类型变了: %T", c.user)
	}

	payloads := []string{"ping", "pong!"}
	total := 0
	for _, payload := range payloads {
		n, err := c.Write([]byte(payload))
		if err != nil {
			t.Fatalf("Write(%q): %v", payload, err)
		}
		if n != len(payload) {
			t.Fatalf("Write(%q) 返回 %d，期望 %d", payload, n, len(payload))
		}
		total += len(payload)
	}

	if sent, _ := user.GetTraffic(); sent != uint64(total) {
		t.Fatalf("用户上行流量 = %d，期望 %d：首个负载随帧头写出时被漏记", sent, total)
	}
	if got := c.sent.Load(); got != uint64(total) {
		t.Fatalf("tracker sent = %d，期望 %d", got, total)
	}
}
