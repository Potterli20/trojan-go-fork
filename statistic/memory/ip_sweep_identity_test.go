package memory

import (
	"testing"
	"time"
)

// TestSweptSlotDelIPDoesNotEvictReplacement 钉住 IP 槽位的身份语义：
// sweepStaleIPs 对超时槽位是**强制**回收（refs>0 也删，这是 DelIP 漏调的兜底）。
// 旧实现里，旧连接 A 占用的 slot 被 sweep 后，同 IP 的新连接 C 又新建了 slot；
// A 关闭时调 DelIP(X)，只按字符串找到 C 的 slot 并 refs--，把 C 还在用的槽位删掉，
// ipNum 于是少于真实活跃 IP 数——MaxIPNum 被绕过，来自另一个 IP 的连接被错误放行。
//
// 判据全程白盒、不依赖墙钟：手动把 slot.lastSeen 拨旧并 sweep，再重入同 IP，
// 然后模拟旧连接的 DelIP。变异对照：DelIP 不识别"被 sweep 过的旧引用"，本用例转红。
func TestSweptSlotDelIPDoesNotEvictReplacement(t *testing.T) {
	u := newIPUser(1)
	defer u.cancel()

	const ip = "203.0.113.9"
	// A：第一条连接占用 X。
	if !u.AddIP(ip) {
		t.Fatal("A AddIP 失败")
	}
	// A 空闲超时，被兜底扫描强制回收（refs 仍为 1）。
	u.ipLock.Lock()
	slotA := u.ipTable[ip]
	if slotA == nil {
		u.ipLock.Unlock()
		t.Fatal("A 的槽位不存在，夹具失效")
	}
	slotA.lastSeen = time.Now().Add(-ipStaleAfter - time.Second)
	u.sweepStaleIPs(time.Now())
	u.ipLock.Unlock()
	if got := u.GetIP(); got != 0 {
		t.Fatalf("sweep 后 GetIP 应为 0，实际 %d", got)
	}

	// C：同 IP 新连接重建槽位（MaxIPNum=1，应成功）。
	if !u.AddIP(ip) {
		t.Fatal("C 重新占用同 IP 失败")
	}
	// 此时任何另一个 IP 都应被拒（槽位被 C 占着）。
	if u.AddIP("198.51.100.77") {
		t.Fatal("C 占着唯一槽位时，另一个 IP 不该被放行")
	}

	// A 这时才关闭，调 DelIP(X)：它只能抵扣自己那份被 sweep 掉的旧引用，
	// 绝不能删掉 C 正在用的新槽位。
	if !u.DelIP(ip) {
		t.Fatal("旧连接的 DelIP 应返回 true（它确实持有过一份引用）")
	}
	if got := u.GetIP(); got != 1 {
		t.Fatalf("旧连接的 DelIP 误删了新连接 C 的槽位：GetIP()=%d，应为 1", got)
	}
	if u.AddIP("198.51.100.78") {
		// 这条是连带后果确认：如果做到这里仍放行，说明 MaxIPNum 已被绕过。
		t.Fatal("C 仍持有一份槽位，MaxIPNum=1 时另一个 IP 不该被放行")
	}

	// C 关闭，槽位才真正释放。
	if !u.DelIP(ip) {
		t.Fatal("C 的 DelIP 应释放槽位")
	}
	if got := u.GetIP(); got != 0 {
		t.Fatalf("最后一条连接关闭后 GetIP()=%d，应为 0", got)
	}
}
