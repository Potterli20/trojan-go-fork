package proxy

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type signalTestSink struct{}

func (*signalTestSink) DialConn(*tunnel.Address, tunnel.Tunnel) (tunnel.Conn, error) {
	return nil, os.ErrClosed
}
func (*signalTestSink) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, os.ErrClosed
}
func (*signalTestSink) Close() error { return nil }

// 建栈期（栈组装、geo 加载、诱饵探测、MySQL 连接）可能持续数秒。旧实现里
// signal.Notify 要等 Run 才安装，这段时间的 SIGTERM 会按默认处置直接杀进程。
// 判据用一个阻塞在 creator 里的假 run-type：信号在建栈窗口内到达，进程必须
// 存活并在 Run 启动时消费掉它、走优雅关停。
func TestSignalDuringBuildIsHandled(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	RegisterProxyCreator("SIGNALTEST", func(ctx context.Context) (*Proxy, error) {
		close(entered)
		<-release
		pctx, cancel := context.WithCancel(ctx)
		return NewProxy(pctx, cancel, nil, &signalTestSink{}), nil
	})

	go func() {
		<-entered
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			t.Errorf("send SIGTERM: %v", err)
		}
		close(release)
	}()

	done := make(chan error, 1)
	go func() {
		p, err := NewProxyFromConfigData([]byte(`{"run_type":"SIGNALTEST"}`), true)
		if err != nil {
			done <- err
			return
		}
		done <- p.RunAndClose()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("优雅关停返回错误: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("建栈期 SIGTERM 没有被捕获，Run 也没有消费它")
	}
}