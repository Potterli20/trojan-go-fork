package mux

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/xtaci/smux"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

// idleSession 造一个"活着但空闲"的 mux 会话：两端都用真实 TCP，
// 避免 net.Pipe 的同步语义让 smux 的后台协程堵在写上。
func idleSession(t *testing.T) (*smux.Session, tunnel.Conn, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	common.Must(err)
	clientConn, err := net.Dial("tcp", ln.Addr().String())
	common.Must(err)
	serverSide, err := ln.Accept()
	common.Must(err)
	ln.Close() //gosec:disable -- 错误忽略：测试清理

	serverSession, err := smux.Server(serverSide, nil)
	if err != nil {
		clientConn.Close() //gosec:disable -- 错误忽略：测试清理
		serverSide.Close() //gosec:disable -- 错误忽略：测试清理
		t.Fatalf("smux.Server: %v", err)
	}
	clientSession, err := smux.Client(clientConn, nil)
	if err != nil {
		clientConn.Close()    //gosec:disable -- 错误忽略：测试清理
		serverSide.Close()    //gosec:disable -- 错误忽略：测试清理
		serverSession.Close() //gosec:disable -- 错误忽略：测试清理
		t.Fatalf("smux.Client: %v", err)
	}
	wrapped := &pipeTunnelConn{Conn: clientConn}
	cleanup := func() {
		clientSession.Close() //gosec:disable -- 错误忽略：测试清理
		serverSession.Close() //gosec:disable -- 错误忽略：测试清理
		clientConn.Close()    //gosec:disable -- 错误忽略：测试清理
		serverSide.Close()    //gosec:disable -- 错误忽略：测试清理
	}
	return clientSession, wrapped, cleanup
}

func newPoolClient(timeout time.Duration) (*Client, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		clientPool: make(map[muxID]*smuxClientInfo),
		timeout:    timeout,
		ctx:        ctx,
		cancel:     cancel,
	}
	return c, cancel
}

func addIdle(c *Client, session *smux.Session, underlay tunnel.Conn, idleFor time.Duration) {
	info := &smuxClientInfo{
		id:           muxID(time.Now().UnixNano()),
		client:       session,
		underlayConn: underlay,
	}
	info.lastActiveTime.Store(time.Now().Add(-idleFor).UnixNano())
	c.clientPoolLock.Lock()
	c.clientPool[info.id] = info
	c.clientPoolLock.Unlock()
}

func poolSize(c *Client) int {
	c.clientPoolLock.Lock()
	defer c.clientPoolLock.Unlock()
	return len(c.clientPool)
}

// TestIdleReaperRunsAtAll 是下限自证：正数 idle-timeout 下，空闲超时的会话必须
// 真的被回收。没有这条，"配错时不回收"那条判据可能只是因为"谁都别被回收"而绿。
func TestIdleReaperRunsAtAll(t *testing.T) {
	session, underlay, cleanup := idleSession(t)
	defer cleanup()

	c, cancel := newPoolClient(200 * time.Millisecond)
	defer cancel()
	addIdle(c, session, underlay, time.Second)

	go c.cleanLoop()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if poolSize(c) == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("空闲会话在 5s 内没有被回收：回收逻辑根本没跑，后面的判据都是空的")
}

// TestNonPositiveIdleTimeoutDoesNotReapLiveSessions 钉的是这次修的缺陷：
// idle-timeout 配成 0（或负数）时，旧代码的回收判据 `空闲 > 0` 恒真，
// 每个空闲会话都会在第一次巡检（兜底 10s）被拆掉，会话复用直接失效。
// 修复后非正数落到文档默认 30s，所以一个空闲 20s 的会话必须还活着。
//
// 观察窗口取 12s：必须大于旧实现的首次巡检（10s），否则这条判据抓不到旧行为。
func TestNonPositiveIdleTimeoutDoesNotReapLiveSessions(t *testing.T) {
	session, underlay, cleanup := idleSession(t)
	defer cleanup()

	c, cancel := newPoolClient(0) // 模拟 mux.idle-timeout: 0
	defer cancel()
	addIdle(c, session, underlay, 20*time.Second)

	go c.cleanLoop()
	time.Sleep(12 * time.Second)
	if got := poolSize(c); got != 1 {
		t.Fatalf("空闲 20s 的会话被回收了（池内剩 %d 个）：非正数的 idle-timeout 让每个空闲会话"+
			"在首次巡检就被拆掉，mux 的会话复用失效", got)
	}
	if session.IsClosed() {
		t.Fatal("会话已被回收协程关闭")
	}
}

// TestIdleTimeoutDefaultIsShared 钉住"没配"与"配错"必须落到同一个时限：
// 配置创建器的默认值和 cleanLoop 的兜底共用一个常量。写死两份数字时，
// 改了其中一份就没人报错——所以这里走真实的解析入口，读实际进 ctx 的那份配置。
func TestIdleTimeoutDefaultIsShared(t *testing.T) {
	ctx, err := config.WithYAMLConfig(context.Background(), []byte("run-type: client\n"))
	if err != nil {
		t.Fatalf("解析空配置失败: %v", err)
	}
	cfg, ok := config.FromContext(ctx, Name).(*Config)
	if !ok || cfg == nil {
		t.Fatal("mux 的配置没有进 ctx")
	}
	if cfg.Mux.IdleTimeout != int(idleDefaultTimeout/time.Second) {
		t.Fatalf("配置默认 IdleTimeout = %d，期望 %d（两处必须共用 idleDefaultTimeout）",
			cfg.Mux.IdleTimeout, int(idleDefaultTimeout/time.Second))
	}
}
