package mysql

// R17-2：SetUsers(Modify) 携带 TrafficTotal 时，api 层把绝对累计量交给
// auth.SetUserTraffic。mysql.Authenticator 内嵌 memory.Authenticator，后者把
// 值写进"自上次刷库的增量"计数器 Sent/Recv 里；下一轮 syncUsers 的
// writeBackTraffic 用 ResetTraffic 取走并 `UPDATE upload=upload+?, download=download+?`，
// 于是这篇绝对量被当成增量重复累加进 DB。设 0 则下轮写回 0，DB 永远停在旧值。
// 同时 quotaBase 没跟着推进，额度判定会少算这段流量，超额用户在下一轮同步前被放行。
//
// 判据用 fakeDB 精确记录 UPDATE 的参数：面板写绝对量之后立即跑一轮
// writeBackTraffic（同步调用，不等墙钟），断言增量里不含那篇绝对量；
// 另设 quota=1000、面板写 900/0，验证后续流量的额度判定当场生效。
// 变异对照：删掉 mysql.SetUserTraffic 覆盖（退回内嵌 memory 实现），本用例转红。

import (
	"testing"
)

func TestSetUserTrafficAbsoluteDoesNotBecomeIncrement(t *testing.T) {
	const hash = "u-traffic-absolute"
	db := &fakeDB{rows: []fakeRow{{
		username: hash, hash: hash, quota: 1000, download: 100, upl: 50,
	}}}
	a := newFakeAuthenticator(t, db)

	// 装载：库侧基线 100/50、额度 1000。
	a.syncUsers()
	if ok, _ := a.AuthUser(hash); !ok {
		t.Fatal("用户未被装载，夹具失效")
	}

	// 面板通过 SetUsers(Modify) 下发绝对累计量 900/0（quota=1000）。
	if err := a.SetUserTraffic(hash, 900, 0); err != nil {
		t.Fatalf("SetUserTraffic: %v", err)
	}
	// 绝对量已落库，内存增量必须清零（否则下轮 flushUser 会重复累加）。
	if sent, recv := a.ListUsers()[0].GetTraffic(); sent != 0 || recv != 0 {
		t.Fatalf("绝对写之后内存增量应清零，实际 %d/%d", sent, recv)
	}

	// 额度判定必须把新绝对量算进去：quota=1000，面板设 900，再产生 50 应放行（950<1000），
	// 再产生 60 应超额（1010>1000），全程不再跑第二轮同步。
	mustUser(t, a, hash).AddSentTraffic(50)
	if ok, _ := a.AuthUser(hash); !ok {
		t.Fatal("面板把累计量设为 900/1000 后，新增 50 未超额却被拒绝：quota 基线算错了")
	}
	mustUser(t, a, hash).AddSentTraffic(60)
	if ok, _ := a.AuthUser(hash); ok {
		t.Fatal("900+110=1010 已超额，AuthUser 仍放行：面板绝对写之后 quota 基线没跟着推进")
	}

	// 清掉装载期与面板写入的调用记录，只看写回阶段。
	db.mu.Lock()
	db.calls = nil
	db.mu.Unlock()

	a.writeBackTraffic() // 模拟下一轮 flush（同步调用，不依赖 30s check_rate）

	// 面板下发的 900 已经落库；下轮写回只能写此后新产生的增量（110/0），
	// 绝不能把 900 再当增量累加一遍。
	for _, call := range db.callsWith("UPDATE `users`") {
		for _, arg := range call.args {
			v, ok := arg.(uint64)
			if !ok {
				continue
			}
			if v != 0 && v != 110 {
				t.Fatalf("面板下发的绝对流量被当成增量写回: UPDATE 参数=%v，"+
					"DB 会被重复累加（900 是最终值，不是新增量）", call.args)
			}
		}
	}
}
