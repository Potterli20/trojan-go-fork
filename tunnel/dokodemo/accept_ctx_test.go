package dokodemo

import (
	"context"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
)

// AcceptConn 必须能被 ctx 取消解除。proxy 的关停顺序是先 p.cancel() 再 wg.Wait()，
// 而 releaseTunnels()（它才会 Close 各 tunnel、进而关掉监听）排在 wg.Wait **之后**；
// 所以只会等监听的 Accept 一旦占着 wg 成员，每次停机都要烧满 5s 的有界兜底
// （forward 模式的 source 就是 DOKODEMO，nat 模式是 TPROXY）。
// 这里只取消 ctx、不调用 Close，判据是 AcceptConn 在有界时间内返回错误。
func TestAcceptConnUnblocksOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	port := common.PickPort("tcp", "127.0.0.1")
	ctx = config.WithConfig(ctx, Name, &Config{
		LocalHost:  "127.0.0.1",
		LocalPort:  port,
		TargetHost: "127.0.0.1",
		TargetPort: 80,
		UDPTimeout: 30,
	})

	server, err := NewServer(ctx, nil)
	common.Must(err)
	defer server.Close() //gosec:disable -- 错误忽略：测试清理

	accepted := make(chan error, 1)
	go func() {
		_, err := server.AcceptConn(&Tunnel{})
		accepted <- err
	}()

	time.Sleep(100 * time.Millisecond) // 让 Accept 确实挂上
	cancel()                           // 模拟 proxy 的 p.cancel()，不碰监听

	select {
	case err := <-accepted:
		if err == nil {
			t.Fatal("取消 ctx 后 AcceptConn 返回了连接，期望返回错误")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("AcceptConn 不感知 ctx：只能等 Close 关监听才能解除，" +
			"而 proxy 的 wg.Wait 排在 releaseTunnels 之前 ⇒ forward/nat 每次停机烧满 5s")
	}
}
