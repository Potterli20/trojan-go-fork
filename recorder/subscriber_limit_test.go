package recorder

import (
	"strconv"
	"testing"
)

// TestSubscriberCountIsBounded 钉住订阅者数量的闸。
//
// `api/service` 的 GetRecords 每条流现生成一个 UUID 当 uid，所以 subscribers 的条目数
// 等于并发尾随流数；而 gRPC API 自身**没有鉴权**（见 api/service/AGENTS.md）。
// maxSubscriberCapacity 管住了单个订阅者的缓冲（4096 条 ≈ 0.5MB），管不住数量：
// 任何能连上 API 的客户端开 N 条流就是 N × 0.5MB 加 N 个协程，且每条流可以一直挂着不退。
//
// 超上限时必须返回错误而不是给一个永远收不到记录的通道——后者对调用方看起来和
// "暂时没有记录"一模一样，是静默故障。
func TestSubscriberCountIsBounded(t *testing.T) {
	// 上限值本身也是契约的一部分：单条 Record 实测 136 字节，一个订阅者的缓冲最多
	// maxSubscriberCapacity 条，所以最坏驻留 ≈ maxSubscribers × maxSubscriberCapacity × 136B。
	// 64 × 4096 × 136B ≈ 35MB，是"无鉴权 API 被开满流"时能接受的量级；再往上调就要
	// 重新算这笔账。这条断言放在最前面，也顺带保证下面的填充循环不会跑成一百万次。
	if maxSubscribers > 1024 {
		t.Fatalf("maxSubscribers = %d：数量上限被调高了，先算一遍最坏驻留"+
			"（× maxSubscriberCapacity %d × 136B/条）再改判据", maxSubscribers, maxSubscriberCapacity)
	}

	uids := make([]string, 0, maxSubscribers+1)
	t.Cleanup(func() {
		for _, uid := range uids {
			Unsubscribe(uid)
		}
	})

	for i := range maxSubscribers {
		uid := "limit-" + strconv.Itoa(i)
		ch, err := Subscribe(uid, "", "", false)
		if err != nil {
			t.Fatalf("第 %d 个订阅者被拒（上限 %d）: %v", i, maxSubscribers, err)
		}
		if ch == nil {
			t.Fatalf("第 %d 个订阅者拿到 nil 通道却没有错误", i)
		}
		uids = append(uids, uid)
	}
	if got := subscriberCount.Load(); got != maxSubscribers {
		t.Fatalf("订阅者计数 = %d，期望 %d", got, maxSubscribers)
	}

	over := "limit-over"
	ch, err := Subscribe(over, "", "", false)
	if err == nil {
		Unsubscribe(over)
		t.Fatalf("第 %d 个订阅者被接受了：数量没有上限，一个无鉴权的 API 客户端开多少条流就占多少份缓冲", maxSubscribers+1)
	}
	if ch != nil {
		t.Fatal("被拒的订阅却返回了非 nil 通道")
	}

	// 退订一个之后必须腾出名额，否则闸会变成"一次性"的
	Unsubscribe(uids[len(uids)-1])
	uids = uids[:len(uids)-1]
	freed := "limit-freed"
	ch, err = Subscribe(freed, "", "", false)
	if err != nil {
		t.Fatalf("退订之后仍然被拒: %v", err)
	}
	if ch == nil {
		t.Fatal("退订之后拿到 nil 通道")
	}
	uids = append(uids, freed)

	// Add 的空订阅短路依赖这个计数，顺手钉住它没有被子/漏减
	for _, uid := range uids {
		Unsubscribe(uid)
	}
	uids = nil
	if got := subscriberCount.Load(); got != 0 {
		t.Fatalf("全部退订之后计数 = %d，期望 0：Add 的空订阅短路会永久失效", got)
	}
}
