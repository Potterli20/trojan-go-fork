package redirector

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestDefaultDialHonorsCanceledContext(t *testing.T) {
	orig := dialTCPContext
	defer func() { dialTCPContext = orig }()

	gotCanceled := false
	dialTCPContext = func(ctx context.Context, d *net.Dialer, network, address string) (net.Conn, error) {
		gotCanceled = ctx.Err() != nil
		return d.DialContext(ctx, network, address)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := defaultDialContext(ctx, &net.Dialer{Timeout: 30 * time.Second}, &net.TCPAddr{}); err == nil {
		t.Fatal("已取消 ctx 下拨号仍返回成功；ctx 没有接到拨号链路")
	}
	if !gotCanceled {
		t.Fatal("defaultDialContext 没有把会话 ctx 传给 DialContext")
	}
}
