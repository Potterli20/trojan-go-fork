package memory

import (
	"context"
	"testing"
	"time"
)

// newIPUser 造一个只用于 IP 计数测试的 User：不起 speedUpdater/ipCleaner，
// 时间推进由测试自己喂给 sweepStaleIPs，免得判据依赖 5s/10s 的真实等待。
func newIPUser(max int) *User {
	ctx, cancel := context.WithCancel(context.Background())
	return &User{
		MaxIPNum: max,
		ipTable:  make(map[string]*ipSlot),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// TestSharedSourceIPKeepsItsSlot 是这次缺陷的门禁。
//
// AddIP/DelIP 按连接调用，而计数按"不同源 IP 的个数"。旧实现没有引用计数，
// 同一个源 IP 的第二条连接只刷新 lastSeen、不增加计数，于是**任意一条**连接关闭时的
// DelIP 都会把整条表项删掉、计数归零，而另一条连接还活着。后果是 MaxIPNum 提前失效：
// NAT 后多个用户共用一个出口 IP、或者客户端自带连接池，都能在同一条 IP 上反复
// "占用—释放"同一个槽位，把上限当摆设。
//
// HEAD 上这条是红的：第一条连接关闭后 GetIP()==0，第二个源 IP 直接被放行。
func TestSharedSourceIPKeepsItsSlot(t *testing.T) {
	u := newIPUser(1)
	defer u.cancel()

	const shared = "203.0.113.7" // 同一个出口 IP 上的两条连接
	if !u.AddIP(shared) {
		t.Fatal("第一条连接占用槽位失败")
	}
	if !u.AddIP(shared) {
		t.Fatal("第二条同 IP 连接应被允许（它不占新槽位）")
	}

	if !u.DelIP(shared) {
		t.Fatal("DelIP 对已登记的 IP 应返回 true")
	}
	if got := u.GetIP(); got != 1 {
		t.Fatalf("一条连接关闭后槽位必须仍然被占住（还有一条在用），实际 GetIP()=%d", got)
	}
	if u.AddIP("198.51.100.4") {
		t.Fatal("MaxIPNum=1 却被绕过：同 IP 的另一条连接还在用，槽位不该已经空出来")
	}

	// 最后一条也关掉，槽位才真正释放
	if !u.DelIP(shared) {
		t.Fatal("第二条连接的 DelIP 应返回 true")
	}
	if got := u.GetIP(); got != 0 {
		t.Fatalf("引用归零后应释放槽位，实际 GetIP()=%d", got)
	}
	if !u.AddIP("198.51.100.4") {
		t.Fatal("槽位释放后新 IP 应可占用")
	}
}

// TestIPLimitInvariantNeverNegative 钉住两件容易一起改错的事：
// 上限的判定仍然精确（不多不少），以及重复 DelIP 不会把计数打到负数。
// ipNum 必须恒等于 len(ipTable)——递增只在插入表项时发生，递减只在删除时发生，
// 这条不变量成立，"打到负数等于上限永久失效"就不可能发生。
func TestIPLimitInvariantNeverNegative(t *testing.T) {
	u := newIPUser(2)
	defer u.cancel()

	if !u.AddIP("a") || !u.AddIP("a") || !u.AddIP("b") {
		t.Fatal("a(两条连接)/b 都应占用成功")
	}
	if u.AddIP("c") {
		t.Fatal("已达 MaxIPNum=2，c 应被拒")
	}

	// 重复释放同一 IP：第二次起必须拿到 false，且计数不再变化
	if !u.DelIP("a") {
		t.Fatal("第一次 DelIP(a) 应为 true")
	}
	if !u.DelIP("a") {
		t.Fatal("第二次 DelIP(a) 应为 true（这是 a 的最后一条连接）")
	}
	if u.DelIP("a") {
		t.Fatal("第三次 DelIP(a) 已无表项，应返回 false 而不是继续递减")
	}
	if u.DelIP("never-added") {
		t.Fatal("未登记的 IP 应返回 false")
	}

	if got := u.GetIP(); got != 1 {
		t.Fatalf("只剩 b，GetIP() 应为 1，实际 %d", got)
	}
	if int(u.ipNum.Load()) != len(u.ipTable) {
		t.Fatalf("不变量被破坏：ipNum=%d len(ipTable)=%d", u.ipNum.Load(), len(u.ipTable))
	}
	if !u.AddIP("c") {
		t.Fatal("已腾出一个槽位，c 应可占用")
	}
	if u.AddIP("d") {
		t.Fatal("重新达到上限，d 应被拒")
	}
}

// TestStaleSweepStillReclaimsLeakedRefs 是这次改动的反向保险。
//
// 加引用计数很容易顺手把上限改"准"、却把 DelIP 漏调的兜底弄没：refs>0 的表项如果
// 只等 DelIP 归零才删，一次漏调就是**永久**占住槽位——那比原来"偏松"更糟。
// ipCleaner/sweepStaleIPs 的强制回收必须照旧生效，即使引用还挂着。
func TestStaleSweepStillReclaimsLeakedRefs(t *testing.T) {
	u := newIPUser(1)
	defer u.cancel()

	if !u.AddIP("leaky") || !u.AddIP("leaky") {
		t.Fatal("占用失败")
	}
	// 模拟两条连接都没有走到 DelIP（认证路径提前 return、进程内 panic 被吞等）

	u.ipLock.Lock()
	slot, found := u.ipTable["leaky"]
	if !found {
		u.ipLock.Unlock()
		t.Fatal("表项不该已经消失")
	}
	if slot.refs != 2 {
		u.ipLock.Unlock()
		t.Fatalf("引用数应为 2，实际 %d", slot.refs)
	}
	slot.lastSeen = time.Now().Add(-ipStaleAfter - time.Second)
	u.sweepStaleIPs(time.Now())
	u.ipLock.Unlock()

	if got := u.GetIP(); got != 0 {
		t.Fatalf("兜底扫描必须强制收回泄漏的引用，实际 GetIP()=%d", got)
	}
	if len(u.ipTable) != 0 {
		t.Fatalf("兜底扫描后表项应清空，实际剩 %d 条", len(u.ipTable))
	}
	if !u.AddIP("next") {
		t.Fatal("泄漏槽位被回收后，新 IP 应能占用")
	}
}
