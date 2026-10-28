package tproxy

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// TestDeliverToSessionReportsOutcome 钉住"投递结果"这个返回值本身：调用方靠它决定日志措辞，
// 而归还责任也跟着它走——false 这条路必须已经把借出的负载还回池，之后调用方不得再碰那块
// buffer（跨层口径同 tunnel/socks 与 tunnel/dokodemo 的 no_retain 判据）。
//
// 只构造会话对象、不起守护协程：这条判据问的是"满队列时还不还、空队列时说不说成功"，
// 与 socket/目的地缓存那半无关。
func TestDeliverToSessionReportsOutcome(t *testing.T) {
	base := borrowedPacketBufs()
	meta := &tunnel.Metadata{Address: &tunnel.Address{
		DomainName:  "example.com",
		Port:        443,
		AddressType: tunnel.DomainName,
	}}
	session := &PacketConn{
		input:  make(chan *packetInfo, udpSessionQueueSize),
		output: make(chan *packetInfo, udpSessionQueueSize),
		ctx:    context.Background(),
		cancel: func() {},
		src:    &net.UDPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 40000},
	}

	// 空队列：投递成功，借用权交给会话，借出计数因此仍比基线高 1
	payload := getPacketBuf()
	if !deliverToSession(session, &packetInfo{metadata: meta, payload: payload[:64]}) {
		putPacketBuf(payload)
		t.Fatal("空队列上的投递被判成失败")
	}
	if got := len(session.input); got != 1 {
		t.Fatalf("成功投递后队列长度 %d，期望 1", got)
	}
	if got := borrowedPacketBufs(); got != base+1 {
		t.Fatalf("投递成功时借出计数 %d（基线 %d）：归还责任已交给会话，这里不该动", got, base)
	}
	putPacketBuf((<-session.input).payload)

	for range udpSessionQueueSize {
		buf := getPacketBuf()
		if !deliverToSession(session, &packetInfo{metadata: meta, payload: buf[:8]}) {
			t.Fatal("填充队列途中被判失败")
		}
	}

	// 满队列：必须返回 false，且在返回之前就就地归还。此刻队列里还压着
	// udpSessionQueueSize 块合法借出的负载，所以期望值是 base+队列深度，不是 base。
	dropped := getPacketBuf()
	if deliverToSession(session, &packetInfo{metadata: meta, payload: dropped[:8]}) {
		t.Fatal("队列已满却报投递成功：日志会把丢包说成 sent")
	}
	if got, want := borrowedPacketBufs(), base+int64(udpSessionQueueSize); got != want {
		t.Fatalf("投递失败后借出计数 %d，期望 %d（基线 %d + 队列里 %d 块）：default 分支漏了归还",
			got, want, base, udpSessionQueueSize)
	}
	for range udpSessionQueueSize {
		putPacketBuf((<-session.input).payload)
	}
	if got := borrowedPacketBufs(); got != base {
		t.Fatalf("清空队列后借出计数 %d（基线 %d）", got, base)
	}
}

// 接线门禁：投递结果必须真的决定日志措辞。这条判据做不出稳定的端到端形态（要捕获全局
// logger，还得和同包其它用例的日志输出并发），照 tunnel/http/h2_status_clamp_test.go 的先例
// 改成读源码断言接线——去掉任何一侧接线，这里立刻红。
func TestDispatchLogFollowsDeliveryOutcome(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("拿不到本文件名，门禁失效")
	}
	src, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "server.go"))
	if err != nil {
		t.Fatalf("读 server.go: %v", err)
	}
	text := string(src)

	if !strings.Contains(text, "delivered := deliverToSession(conn,") {
		t.Fatal("dispatch 没有接上 deliverToSession 的返回值：丢弃与投递两条出口又会共用一句日志")
	}
	if !strings.Contains(text, "size := len(info.payload)") {
		t.Fatal("没有把 size 提到投递之前：Put 之后再读那块 buffer 就违反归还后的禁碰口径")
	}
	if strings.Contains(text, `"tproxy packet sent with metadata", info.dst, "size", len(info.payload)`) {
		t.Fatal("仍在投递/归还之后再读 info.payload 记日志")
	}
	guard := strings.Index(text, "if delivered {")
	sentLine := strings.Index(text, `"tproxy packet sent with metadata"`)
	dropLine := strings.Index(text, `"tproxy dropped packet`)
	if guard < 0 || sentLine < 0 || dropLine < 0 {
		t.Fatal("日志没有按 delivered 分流：既找不到分支也找不到丢弃那条")
	}
	if !(guard < sentLine && sentLine < dropLine) {
		t.Fatal("丢弃的包不能再报成 sent：措辞必须由 delivered 决定")
	}
}
