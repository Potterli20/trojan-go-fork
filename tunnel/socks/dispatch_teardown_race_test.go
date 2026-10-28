package socks

import (
	"context"
	"net"
	"sync"
	"testing"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// TestDispatchDoesNotDeliverAfterSessionTeardown 钉住「查表与投递必须在同一把读锁内」
// 这条不变量（口径见 tunnel/tproxy/server.go 与 tunnel/dokodemo/server.go 的同处注释）。
//
// 会话守护协程的收尾是「写锁 delete → conn.Close() 排空 input」。dispatch 如果在解锁之后
// 才投递，就可能落在"已 delete、排空也已经跑完"之后：这一包会成功入队却再无读者，从池里
// 借出的 8KB 负载永久不归还（抽干池之后每包重新 make，池化等于白做），而 packetInfo 连同
// metadata/Address 一起滞留。触发它只需要客户端在拆会话的瞬间继续发包——源地址还能伪造，
// socks 的 UDP 入口在 ASSOCIATE 之外对数据包本身不做任何认证。
//
// 判据用包级同步点 dispatchGap 把守护协程的收尾顺序逐字插在"已查到会话、尚未投递"的窗口
// 正中，所以红绿与墙钟无关：漏归还在这条测试里是永久性的（会话已不在 mapping 里，也没有
// 守护协程会再来排空它），3s 轮询取到的是确定答案而不是运气。
func TestDispatchDoesNotDeliverAfterSessionTeardown(t *testing.T) {
	base := borrowedPacketBufs()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	listen := &multiSrcListenConn{feed: make(chan srcPacket, 8), closed: make(chan struct{})}
	s, _, _ := newSeededServer(ctx, listen, 0)

	src := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 50123}
	key := tunnel.NewSessionKey(src)
	session := &PacketConn{
		input:  make(chan *packetInfo, udpSessionQueueSize),
		output: make(chan *packetInfo, udpSessionQueueSize),
		ctx:    ctx,
		// 刻意留空 cancel，也不起守护协程：这条会话的"拆"完全由测试在 dispatchGap 里按
		// 守护协程的顺序代管（写锁 delete → Close 排空）。Close 里的 cancel 因此是无副作用
		// 的占位——判据要复现的是"排空已经跑完之后又来一包"这个时序，而不是 ctx 状态。
		cancel: func() {},
		src:    src,
	}
	s.mappingLock.Lock()
	s.mapping[key] = session
	s.mappingLock.Unlock()

	var once sync.Once
	tornDown := make(chan struct{})
	restore := dispatchGap
	dispatchGap = func() {
		once.Do(func() {
			defer close(tornDown)
			s.mappingLock.Lock()
			delete(s.mapping, key)
			s.mappingLock.Unlock()
			session.Close()
		})
	}
	t.Cleanup(func() { dispatchGap = restore })

	listen.feed <- srcPacket{payload: socksPacket("race me"), src: src}
	go s.packetDispatchLoop()

	<-tornDown
	if !waitOutstanding(base) {
		if stuck := len(session.input); stuck > 0 {
			t.Fatalf("会话已拆除、排空也跑完之后又投进 %d 包：查表与投递不在同一把读锁内，"+
				"借出的负载永久离开池（每命中一次漏一块 8KB）", stuck)
		}
		t.Fatalf("借出计数停在 %d（起始 %d），但会话队列是空的：归还路径不对", borrowedPacketBufs(), base)
	}
	if got := len(session.input); got != 0 {
		t.Fatalf("拆会话之后又投进 %d 包，即便账目对平也是把包塞给了已死的会话", got)
	}
}
