package trojan

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/statistic/memory"
	"github.com/Potterli20/trojan-go-fork/tunnel/transport"
)

// TestMisdirectionProbeHonorsContext 钉住启动期诱饵探测接回 ctx 并带超时。
//
// 旧写法是裸 `net.Dial("tcp", redirAddr.String())`：没有超时、完全不看 ctx。诱饵地址
// 丢 SYN（黑洞 IP 就是这个形态）时 NewServer 里的这次同步探测要挂满约 2 分钟才报错，
// 期间建栈的主协程被钉住，Ctrl-C 与上层 cancel 全都无效；域名诱饵还会再叠一轮不看 ctx
// 的 DNS。同仓 redirector.defaultDial 早就改成 30s 超时，这两处探测漏改了。
//
// 判据刻意用一个**活着**的诱饵监听：旧写法这次 dial 会成功、于是 NewServer 走下去并
// 返回 nil error——把 ctx 传进拨号才是这里要钉的东西，而"连不上"是既有的另一条路径。
// 用已取消的 ctx 让它变得确定：不接 ctx 的实现会连上，接了 ctx 的实现必须立刻失败，
// 且失败原因确实是那道探测（不是后面某个步骤的副产物）。
func TestMisdirectionProbeHonorsContext(t *testing.T) {
	Auth = nil

	decoy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen decoy: %v", err)
	}
	defer decoy.Close() //gosec:disable -- 错误忽略：测试清理
	host, portStr, err := net.SplitHostPort(decoy.Addr().String())
	if err != nil {
		t.Fatalf("split %v: %v", decoy.Addr(), err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port %q: %v", portStr, err)
	}

	localPort := common.PickPort("tcp", "127.0.0.1")
	ctx := config.WithConfig(context.Background(), transport.Name, &transport.Config{
		LocalHost:  "127.0.0.1",
		LocalPort:  localPort,
		RemoteHost: "127.0.0.1",
		RemotePort: localPort,
	})
	underlay, err := transport.NewServer(ctx, nil)
	common.Must(err)
	defer underlay.Close() //gosec:disable -- 错误忽略：测试清理

	// DisableHTTPCheck 必须留 false：这条探测正是被它管着的那一段
	ctx = config.WithConfig(ctx, memory.Name, &memory.Config{Passwords: []string{"password"}})
	ctx = config.WithConfig(ctx, Name, &Config{
		LocalHost:  "127.0.0.1",
		LocalPort:  common.PickPort("tcp", "127.0.0.1"),
		RemoteHost: host,
		RemotePort: port,
	})
	cancelled, cancel := context.WithCancel(ctx)
	cancel() // 探测必须被这次取消打断，而不是照样连上去

	start := time.Now()
	server, err := NewServer(cancelled, underlay)
	elapsed := time.Since(start)
	if err == nil {
		server.Close() //gosec:disable -- 错误忽略：测试清理
		t.Fatal("ctx 已取消而诱饵探测照样成功：裸 net.Dial 不看 ctx，" +
			"换成黑洞诱饵时这里会挂满约 2 分钟，期间 Ctrl-C 无效")
	}
	if !strings.Contains(err.Error(), "invalid redirect address") {
		t.Fatalf("失败原因不是诱饵探测（%v）：判据没走到要钉的那一步", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("取消之后过了 %v 才返回：探测拨号没有接 ctx", elapsed.Round(time.Millisecond))
	}
}
