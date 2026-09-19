package proxy

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

func TestBoundedBufPoolReuseAndResidencyLimit(t *testing.T) {
	pool := newBoundedBufPool(128, 2)

	buf := pool.Get()
	if len(buf) != 128 || cap(buf) != 128 {
		t.Fatalf("Get returned buffer len=%d cap=%d, want 128", len(buf), cap(buf))
	}
	pool.Put(buf)
	if n := len(pool.bufs); n != 1 {
		t.Fatalf("pool holds %d buffers after Put, want 1", n)
	}

	reused := pool.Get()
	if &reused[0] != &buf[0] {
		t.Fatal("Get did not reuse the pooled buffer")
	}
	if len(reused) != 128 {
		t.Fatalf("reused buffer has len=%d, want 128", len(reused))
	}

	// 驻留上限：超出的 Put 直接丢弃，不阻塞
	first, second, third := pool.Get(), pool.Get(), pool.Get()
	pool.Put(first)
	pool.Put(second)
	pool.Put(third)
	if n := len(pool.bufs); n != 2 {
		t.Fatalf("pool holds %d buffers, want limit 2", n)
	}

	// 尺寸不符的 buffer 不能进池，否则会被别的转发路径按错误长度使用
	pool.Put(make([]byte, 64, 64))
	if n := len(pool.bufs); n != 2 {
		t.Fatalf("wrong-capacity buffer was pooled: %d", n)
	}
}

func TestBoundedBufPoolDisabled(t *testing.T) {
	pool := newBoundedBufPool(128, 0)
	pool.Put(pool.Get())
	if n := len(pool.bufs); n != 0 {
		t.Fatalf("limit<=0 must disable pooling, pool holds %d", n)
	}
	if len(pool.Get()) != 128 {
		t.Fatal("Get must still return a usable buffer when pooling is disabled")
	}
}

func TestNewProxyPacketPoolCoversMaxPacketSize(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 默认配置：转发 buffer 已经容得下一个 UDP 包，两个路径共用一个池
	p := NewProxy(ctx, cancel, nil, nil)
	if p.bufPool.size != defaultBufSize {
		t.Fatalf("default buffer size = %d, want %d", p.bufPool.size, defaultBufSize)
	}
	if p.packetPool != p.bufPool {
		t.Fatal("packet pool should be shared when the relay buffer is large enough")
	}

	// 调小 relay_buffer_size 时，UDP 路径仍必须拿到能装下整包的 buffer：
	// trojan 的 ReadWithMetadata 在 buffer 小于包长时返回错误，会断开整条 packet 流
	ctx = config.WithConfig(ctx, Name, &Config{
		RelayBufferSize:  1024,
		RelayBufferCount: 16,
	})
	p = NewProxy(ctx, cancel, nil, nil)
	if p.bufPool.size != 1024 {
		t.Fatalf("relay buffer size = %d, want 1024", p.bufPool.size)
	}
	if p.packetPool == p.bufPool {
		t.Fatal("packet pool must not reuse an undersized relay buffer")
	}
	if p.packetPool.size < MaxPacketSize {
		t.Fatalf("packet buffer size = %d, want at least %d", p.packetPool.size, MaxPacketSize)
	}
	if p.packetPool.limit != 16 {
		t.Fatalf("packet pool limit = %d, want 16", p.packetPool.limit)
	}
}

// fakeServer 和 fakeClient 只用于记录 Close 是否被调用。closed 用原子量：
// 超时路径下后台的释放 goroutine 可能还在写它。
type fakeServer struct {
	closed atomic.Bool
}

func (s *fakeServer) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	return nil, io.EOF
}

func (s *fakeServer) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, io.EOF
}

func (s *fakeServer) Close() error {
	s.closed.Store(true)
	return nil
}

type fakeClient struct {
	closed atomic.Bool
}

func (c *fakeClient) DialConn(*tunnel.Address, tunnel.Tunnel) (tunnel.Conn, error) {
	return nil, io.EOF
}

func (c *fakeClient) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, io.EOF
}

func (c *fakeClient) Close() error {
	c.closed.Store(true)
	return nil
}

func newTestProxy(t *testing.T) *Proxy {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return NewProxy(ctx, cancel, nil, nil)
}

func TestWaitBoundedReturnsWhenWorkFinishes(t *testing.T) {
	if !waitBounded(time.Minute, nil, "noop", func() {}) {
		t.Fatal("waitBounded must succeed when fn returns before the timeout")
	}
}

func TestWaitBoundedTimesOut(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	start := time.Now()
	if waitBounded(50*time.Millisecond, nil, "stuck work", func() { <-release }) {
		t.Fatal("waitBounded must report failure when fn is still stuck at the timeout")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("waitBounded blocked for %v, the timeout was not applied", elapsed)
	}
}

func TestWaitBoundedUnblockedBySignal(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	sig := make(chan os.Signal, 1)
	sig <- os.Interrupt
	start := time.Now()
	if waitBounded(time.Minute, sig, "stuck work", func() { <-release }) {
		t.Fatal("a signal must end the wait early")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("waitBounded waited %v after the signal, want immediate return", elapsed)
	}
}

// TestCloseReleasesResourcesWhenRelayStuck 是核心回归：转发 goroutine 永久卡死时
// Close 仍必须超时返回并关掉 sink/source，否则监听端口与文件句柄会一直挂着。
func TestCloseReleasesResourcesWhenRelayStuck(t *testing.T) {
	old := shutdownTimeout
	shutdownTimeout = 50 * time.Millisecond
	t.Cleanup(func() { shutdownTimeout = old })

	sink := &fakeClient{}
	source := &fakeServer{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	p := NewProxy(ctx, cancel, []tunnel.Server{source}, sink)
	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) })
	p.wg.Go(func() { <-stuck })

	if err := p.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if !sink.closed.Load() {
		t.Fatal("Close must close the sink even when the relay is stuck")
	}
	if !source.closed.Load() {
		t.Fatal("Close must close every source even when the relay is stuck")
	}
	if ctx.Err() == nil {
		t.Fatal("Close must cancel the proxy context")
	}
}

// erroringServer 的 Close 总是失败，用于验证关闭错误会向上传播
type erroringServer struct {
	fakeServer
}

func (s *erroringServer) Close() error {
	s.closed.Store(true)
	return errors.New("listener still busy")
}

// TestClosePropagatesTunnelErrors 保证「端口没关掉」不会被当成干净退出：
// main 只在 Handle 返回 nil 时以退出码 0 结束，错误必须一路冒泡上去。
func TestClosePropagatesTunnelErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sink := &fakeClient{}
	source := &erroringServer{}
	p := NewProxy(ctx, cancel, []tunnel.Server{source}, sink)

	err := p.Close()
	if err == nil {
		t.Fatal("Close must report a failing tunnel Close")
	}
	if !strings.Contains(err.Error(), "listener still busy") {
		t.Fatalf("Close error = %v, want it to wrap the underlying error", err)
	}
	if !sink.closed.Load() {
		t.Fatal("a failing source Close must not skip the sink")
	}
}

func TestWatchShutdownSignalIsIdempotent(t *testing.T) {
	p := newTestProxy(t)
	first := p.watchShutdownSignal()
	if first == nil {
		t.Fatal("watchShutdownSignal must install a signal channel")
	}
	defer signal.Stop(first)
	if second := p.watchShutdownSignal(); second != first {
		t.Fatal("watchShutdownSignal must reuse the same channel; a second Notify would double-deliver signals")
	}
}

// blockingAcceptServer 模拟等待新连接的 acceptLoop：Accept 进入后向 entered 上报一次，
// 然后一直阻塞到测试结束。测试据此确认 relay loop 的 wg.Add 已经发生，
// 避免把「Add 与 Wait 并发」这种 WaitGroup 误用带进用例里。
type blockingAcceptServer struct {
	fakeServer
	entered chan struct{}
	release chan struct{}
}

func (s *blockingAcceptServer) enteredNow() {
	select {
	case s.entered <- struct{}{}:
	default:
	}
}

func (s *blockingAcceptServer) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	s.enteredNow()
	<-s.release
	return nil, io.EOF
}

func (s *blockingAcceptServer) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	s.enteredNow()
	<-s.release
	return nil, io.EOF
}

// TestRunReturnsWhenCloseRunsInAnotherGoroutine 覆盖 option.go/easy.go 的实际用法：
// Run 跑在 goroutine 里、Close 从别处调用。信号句柄必须在构造时就准备好，
// 惰性创建会让 Run 的 Notify 与 Close 的读取产生数据竞争。
func TestRunReturnsWhenCloseRunsInAnotherGoroutine(t *testing.T) {
	old := shutdownTimeout
	shutdownTimeout = 50 * time.Millisecond
	t.Cleanup(func() { shutdownTimeout = old })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	source := &blockingAcceptServer{entered: make(chan struct{}, 2), release: make(chan struct{})}
	t.Cleanup(func() { close(source.release) })
	sink := &fakeClient{}
	p := NewProxy(ctx, cancel, []tunnel.Server{source}, sink)

	done := make(chan error, 1)
	go func() { done <- p.Run() }()
	// 等 TCP 与 UDP 两个 acceptLoop 都跑起来
	for range 2 {
		select {
		case <-source.entered:
		case <-time.After(2 * time.Second):
			t.Fatal("relay loops did not start")
		}
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run must return once Close cancels the context")
	}
	if !sink.closed.Load() {
		t.Fatal("Close must release the tunnels")
	}
}

// TestCloseDiscardsSignalBufferedBeforeCleanup 验证清理开始前积压的信号不会被误当成
// 「等不及」的二次信号，否则第一次关闭请求就会把优雅等待直接跳过。
func TestCloseDiscardsSignalBufferedBeforeCleanup(t *testing.T) {
	old := shutdownTimeout
	shutdownTimeout = 10 * time.Second
	t.Cleanup(func() { shutdownTimeout = old })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sink := &fakeClient{}
	source := &fakeServer{}
	p := NewProxy(ctx, cancel, []tunnel.Server{source}, sink)

	sig := p.watchShutdownSignal()
	defer signal.Stop(sig)
	// 信号在 Close 之前到达，留在缓冲里
	sig <- os.Interrupt
	// 转发 goroutine 在 ctx 取消后还要 200ms 才退出
	p.wg.Go(func() {
		<-ctx.Done()
		time.Sleep(200 * time.Millisecond)
	})

	start := time.Now()
	if err := p.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("Close returned after %v; the pending signal was mistaken for a second one", elapsed)
	}
	if !sink.closed.Load() || !source.closed.Load() {
		t.Fatal("Close must still release sink and sources")
	}
}

// blockingServer 的 Close 永久卡死，模拟底层 tunnel 内部 wg.Wait 挂住的情形。
// entered 在阻塞前关闭，用来确认「sink 已经关完」这一步确实执行过了。
type blockingServer struct {
	fakeServer
	entered chan struct{}
	release chan struct{}
}

func (s *blockingServer) Close() error {
	close(s.entered)
	<-s.release
	s.closed.Store(true)
	return nil
}

// TestCloseDoesNotHangWhenTunnelCloseBlocks 覆盖第二个环节：底层 Server.Close
// 自带无上限的 wg.Wait，卡死时 Proxy.Close 也必须超时返回，否则端口和句柄
// 会一直挂着，进程只能等 systemd 的 SIGKILL。
func TestCloseDoesNotHangWhenTunnelCloseBlocks(t *testing.T) {
	old := shutdownTimeout
	shutdownTimeout = 50 * time.Millisecond
	t.Cleanup(func() { shutdownTimeout = old })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sink := &fakeClient{}
	source := &blockingServer{entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() { close(source.release) })
	p := NewProxy(ctx, cancel, []tunnel.Server{source}, sink)

	done := make(chan error, 1)
	go func() { done <- p.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close hung on a blocked tunnel Close")
	}
	<-source.entered // 后台释放流程已走到 sink 之后
	if !sink.closed.Load() {
		t.Fatal("sink must be closed before the blocked source")
	}
}
