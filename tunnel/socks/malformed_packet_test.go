package socks

import (
	"context"
	"net"
	"testing"
	"time"
)

// malformedUDPPacket 是一个长度够过 n<4 闸门、但地址块非法的 SOCKS5 UDP 包：
// RSV(2)+FRAG(1)+ATYP(0xFF=非法)。address.ReadFrom 会在 ATYP 的 default 分支报错。
func malformedUDPPacket() []byte {
	return []byte{0, 0, 0, 0xFF, 'x'}
}

// TestMalformedUDPPacketDoesNotCreateSession 钉住 socks UDP 地址解析的位置：
// 旧实现在 `!found` 分支（建会话、起守护协程、投 packetChan）**之后**才解析地址块，
// 于是一个 ATYP 非法的包就能凭空建出一条空会话——带 2 条深度 16 的通道、一个子
// ctx 和一个守护协程，存活到 UDPTimeout(60s)。源地址可伪造，伪造源高速重放即可
// 顶满 maxUDPSessions，挤掉合法用户（会话上限检查在建会话时已通过，因为那时还
// 不知道这个包是废包）。
//
// 判据用"随后投给一条预置合法会话的包"当同步点（形态同 session_bound_test）：
// 单消费者 dispatch 把同步包投进预置会话的 input 时，前面 N 个畸形包必然都已判完。
// 此时断言 mapping 仍只有预置那 1 条。把解析挪回建会话之后，本用例立刻转红，
// 且与墙钟无关。
func TestMalformedUDPPacketDoesNotCreateSession(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	listen := &multiSrcListenConn{feed: make(chan srcPacket, 16), closed: make(chan struct{})}
	s, existing, _ := newSeededServer(ctx, listen, 1) // 1 条预置会话作同步点

	go s.packetDispatchLoop()

	// 3 个畸形包，源地址互不相同——旧实现会各建一条会话。
	const malformedCount = 3
	for i := range malformedCount {
		src := &net.UDPAddr{IP: net.IPv4(10, 0, 0, byte(i+1)), Port: 4000 + i}
		listen.feed <- srcPacket{payload: malformedUDPPacket(), src: src}
	}
	// 同步包：投给预置会话，收到即表示前面的畸形包都处理完了。
	listen.feed <- srcPacket{payload: socksPacket("sync"), src: existing.src}
	select {
	case <-existing.input:
	case <-time.After(3 * time.Second):
		t.Fatal("dispatch 没有把同步包投递给预置会话，判据失效")
	}

	s.mappingLock.RLock()
	got := len(s.mapping)
	s.mappingLock.RUnlock()
	if got != 1 {
		t.Fatalf("mapping 有 %d 条会话（预置 1 + 畸形包新建 %d）：非法地址块在建会话之前就该被丢弃，"+
			"否则伪造源可用废包顶满会话表", got, got-1)
	}
	// 正向对照：畸形包没能把包投进任何会话；预置会话只收到那一个同步包。
	if extra := len(existing.input); extra != 0 {
		t.Fatalf("预置会话里还剩 %d 个包，说明畸形包也被投递了", extra)
	}
}
