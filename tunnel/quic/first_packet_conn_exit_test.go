package quic

import (
	"os"
	"strings"
	"testing"
)

// QUIC 服务端每条连接有两个数据报泵 select：
//   - 首包投递（s.packetChan <- udpSession）：只等 s.ctx / udpSession.done。
//   - 后续包投递（packetBuffer <- data）：正确地带 connCtx.Done()。
//
// connCtx 由 handleConnection 的 defer connCancel() 结束（streamChan 关闭时主循环 return）。
// 若对端在 packetChan 满（容量 8）时关闭连接，首包 select 因缺 connCtx.Done() 不会醒来，
// udpSession 又尚未交出去、无人关它的 done，泵只能 park 到 s.ctx.Done() —— 每命中一次
// 残留 1 goroutine + 1 连接 + activeConns 条目。第9轮"两个泵都 select connCtx"改造
// 漏了首包这一处。
//
// 端到端构造"packetChan 满 + 连接恰好关闭"的窗口在 CI 上极不稳定（依赖真实 QUIC 连接
// 与调度时序），所以按项目先例（h2_status_clamp_test.go）用精确结构门禁：定位唯一的
// "case s.packetChan <- udpSession:" 所在 select 块，断言该块内含 connCtx.Done()。
// 第二个已正确的 select 不会被误判为通过。
func TestFirstPacketDeliverySelectHasConnExit(t *testing.T) {
	data, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("读 server.go: %v", err)
	}
	src := string(data)
	marker := "case s.packetChan <- udpSession:"
	idx := strings.Index(src, marker)
	if idx < 0 {
		t.Fatal("找不到首包投递分支，判据失效")
	}
	// 从该 case 往前找到最近的 "select {"，往后找到配对的 "}" 结尾。
	selectStart := strings.LastIndex(src[:idx], "select {")
	if selectStart < 0 {
		t.Fatal("首包投递分支不在 select 里，判据失效")
	}
	end := strings.Index(src[idx:], "\n\t\t\t}")
	if end < 0 {
		t.Fatal("找不到首包 select 的结束括号，判据失效")
	}
	block := src[selectStart : idx+end]
	if !strings.Contains(block, "connCtx.Done()") {
		t.Fatalf("首包投递 select 缺 connCtx.Done()：连接先结束 + packetChan 满时泵会永久 park。\n当前块:\n%s", block)
	}
	// 分母自证：同文件第二个 select（后续投递）确实带 connCtx.Done()，证明门禁读的是真代码。
	if !strings.Contains(src, "case <-connCtx.Done():") {
		t.Fatal("连后续投递的 connCtx.Done() 都找不到，门禁失效")
	}
}
