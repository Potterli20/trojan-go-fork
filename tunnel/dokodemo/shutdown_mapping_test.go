package dokodemo

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// TestSessionMapIsEmptiedWhenDownstreamGone 盯的是关停时的最后一个出口：
// 会话已建好、`s.packetChan <- conn` 撞上 ctx.Done。这条会话的守护协程从没起来，
// 删除 mapping 表项只能由这个分支自己做；不做的话 mapping 永久留着一条已 Close 的
// 死会话（两条队列 + 会话 ctx），Close 之后仍被 Server 对象攥着。
// 顺带确认手里那条包已随 conn.Close() 的排空交出会话队列。
//
// 时序做成确定性的：packetChan 用**无缓冲**通道，dispatch 必然卡在投递上；
// 轮询到 mapping 出现那条会话才 cancel，保证判的是投递分支。
//
// 断言看的是这条会话自己的 input 队列长度，而不是包级借出计数：同包前面的用例
// 会留下尚未收尾的 dispatch/守护协程，它们手里的 buffer 在稍后归还，会让全局计数
// 在我们测完之后再降一块（-race 全仓跑时实测过这种跨用例干扰）。
func TestSessionMapIsEmptiedWhenDownstreamGone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	udp := &multiSrcUDP{feed: make(chan udpPacket, 4), closed: make(chan struct{})}
	s := newAccountingServer(ctx, cancel, udp)
	s.packetChan = make(chan tunnel.PacketConn) // 无缓冲：没人接，投递必然阻塞
	done := make(chan struct{})
	go func() {
		s.dispatchLoop()
		close(done)
	}()

	src := &net.UDPAddr{IP: net.IPv4(198, 51, 100, 9), Port: 33001}
	udp.feed <- udpPacket{payload: []byte("no downstream"), src: src}

	deadline := time.Now().Add(3 * time.Second)
	var session *PacketConn
	for {
		s.mappingLock.Lock()
		session = s.mapping[tunnel.NewSessionKey(src)]
		s.mappingLock.Unlock()
		if session != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("dispatch 没有建立会话，用例没走到 packetChan 那一跳，判据失效")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// 同步点：包已经进了会话 input，dispatch 正卡在投递上
	if got := len(session.input); got != 1 {
		t.Fatalf("同步点会话 input 里有 %d 个包，期望 1，判据失效", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel 之后 dispatch 没退出")
	}

	s.mappingLock.Lock()
	left := len(s.mapping)
	s.mappingLock.Unlock()
	if left != 0 {
		t.Fatalf("关停后 mapping 里还剩 %d 条死会话：守护协程没起来，表项无人删除", left)
	}
	if got := len(session.input); got != 0 {
		t.Fatalf("关停时会话 input 还留着 %d 个包：这个出口没有走 conn.Close() 的排空", got)
	}
}
