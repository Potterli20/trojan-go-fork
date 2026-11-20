package mux

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// cleanLoop 的空闲回收判据只看 NumStreams()==0 && idle>timeout，不看 pendingStreams。
// DialConn 在 clientPoolLock 内做了 pendingStreams++、解锁后到 OpenStream 之间，
// NumStreams() 仍为 0 而 lastActiveTime 不前进：cleanLoop 恰好在这段窗口把会话
// 从池中摘掉并关掉底层连接，紧接着的 OpenStream 落在已关闭的 session 上必然失败。
// 用户看到的是"复用一条长空闲会话"这一最该被优化的场景里随机失败一次。
//
// 判据用真实的 cleanLoop goroutine + afterMuxPick 钩子：A 选中会话、预约流位后
// 停在钩子里，此时把 lastActiveTime 拨到过期，让接下来几轮 tick 去回收。
// 修复前会话被摘掉、A 的 OpenStream 失败；修复后 pendingStreams>0 挡住回收。
func TestCleanLoopDoesNotReclaimReservedClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		concurrency: 1,
		timeout:     40 * time.Millisecond, // checkDuration = 10ms
		ctx:         ctx,
		cancel:      cancel,
		clientPool:  make(map[muxID]*smuxClientInfo),
		underlay:    &pipeUnderlay{},
	}
	t.Cleanup(func() { _ = c.Close() })

	info, err := c.newMuxClient()
	if err != nil {
		t.Fatalf("newMuxClient: %v", err)
	}
	c.wg.Go(func() { c.cleanLoop() })

	reached := make(chan struct{})
	release := make(chan struct{})
	var once atomic.Bool
	orig := afterMuxPick
	t.Cleanup(func() { afterMuxPick = orig })
	afterMuxPick = func() {
		if once.CompareAndSwap(false, true) {
			// A 已选中 info、pendingStreams=1、锁已释放、尚未 OpenStream。
			// 此刻把会话拨成"空闲超时"，让 cleanLoop 接下来的 tick 面对这个窗口。
			info.lastActiveTime.Store(time.Now().Add(-time.Hour).UnixNano())
			close(reached)
			<-release
		}
	}

	aDone := make(chan error, 1)
	go func() {
		_, err := c.DialConn(nil, nil)
		aDone <- err
	}()
	select {
	case <-reached:
	case <-time.After(2 * time.Second):
		t.Fatal("协程 A 没有走到选路窗口，判据失效")
	}

	// 让 cleanLoop 在 A 挂起期间跑满十轮 tick：修复前 info 会被判为空闲而摘掉。
	time.Sleep(150 * time.Millisecond)
	c.clientPoolLock.Lock()
	_, stillPooled := c.clientPool[info.id]
	c.clientPoolLock.Unlock()

	close(release)
	select {
	case err := <-aDone:
		if !stillPooled {
			t.Fatal("cleanLoop 在「已预约未开流」窗口回收了会话，A 的 OpenStream 落在已关闭的 session 上")
		}
		if err != nil {
			t.Fatalf("A 拨号失败（会话被 cleanLoop 提前拆掉）: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("协程 A 未返回")
	}
}
