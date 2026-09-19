package proxy

import (
	"context"
	"errors"
	"io"
	"math"

	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

const Name = "PROXY"

// proxyIDKey 是 context value 的自定义 key 类型，避免使用内置 string 类型造成碰撞
type proxyIDKey string

const (
	MaxPacketSize = 1024 * 8
)

type Proxy struct {
	sources    []tunnel.Server
	sink       tunnel.Client
	ctx        context.Context
	cancel     context.CancelFunc
	bufPool    *boundedBufPool
	packetPool *boundedBufPool
	wg         sync.WaitGroup
	logFile    *os.File
	// sigChan 在 NewProxy 里就分配好：Run 与 Close 常常跑在不同 goroutine 上，
	// 惰性创建会与 Close 的读取竞争。Notify 由 Run 挂上、Close 结束时才卸载，
	// 跨住 Run→Close 的间隙，避免清理阶段再收到信号时被默认处置直接杀进程。
	sigChan chan os.Signal
	sigOnce sync.Once
}

// boundedBufPool 带驻留数量上限的转发 buffer 池：
// 池内最多驻留 limit 个 buffer（limit<=0 时不池化），池空或池满时临时分配/直接丢弃，
// 由 GC 回收，从而限制转发层的常驻内存占用。
//
// 注意 limit 约束的是「池内驻留量」而不是「并发借用量」：一个 buffer 被借出后不再
// 计入驻留量，池空时 Get 会照常新分配。所以峰值内存 = 活跃转发方向数 × size，
// 与 limit 无关；limit 的作用是把空闲期回收的 buffer 数量压住，避免转发高峰期
// 之后长期占着 count × size 的内存。这里不做并发上限：中继 goroutine 可能因对端
// 卡死而长期持有 buffer，一旦 Get 阻塞，新连接会在全局限流上排队放大成整站不可用。
type boundedBufPool struct {
	size  int
	limit int
	bufs  chan []byte
}

func newBoundedBufPool(size, limit int) *boundedBufPool {
	if limit < 0 {
		limit = 0
	}
	return &boundedBufPool{
		size:  size,
		limit: limit,
		bufs:  make(chan []byte, limit),
	}
}

func (p *boundedBufPool) Get() []byte {
	select {
	case buf := <-p.bufs:
		return buf
	default:
		return make([]byte, p.size)
	}
}

func (p *boundedBufPool) Put(buf []byte) {
	if cap(buf) != p.size {
		return
	}
	select {
	case p.bufs <- buf[:p.size]:
	default:
	}
}

// shutdownTimeout 是关闭阶段单个环节的最长等待时间（等中继 goroutine 退出、
// 等底层 tunnel 关闭各算一次）。io.CopyBuffer 不感知 ctx，且每个 tunnel Server
// 的 Close 内部还有自己的 wg.Wait：对端半开、mux/QUIC 会话卡死时这些等待都可能
// 永久挂住，无上限就会让进程永远退不出去。
var shutdownTimeout = 5 * time.Second

// waitBounded 在后台执行 fn 并最多等待 timeout；sig 非 nil 时收到信号也立即结束等待。
// 返回 false 表示没等满（超时或提前收到信号），此时 fn 可能仍在后台运行。
// 进程即将退出，遗留的 goroutine 由 runtime 回收；对分步调用的库使用方，
// 走到这一步说明底层已经卡死，继续挂住比留下一个未退出的 goroutine 更糟。
func waitBounded(timeout time.Duration, sig <-chan os.Signal, what string, fn func()) bool {
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case s := <-sig:
		log.Warn("received signal ", s, " while trying to ", what, "; skipping the remaining wait")
		return false
	case <-timer.C:
		log.Warn("timed out after ", timeout, " while trying to ", what)
		return false
	}
}

// watchShutdownSignal 安装 SIGINT/SIGTERM 处理，幂等。
// 句柄由 Run 安装、Close 结束时才卸载，中间不留空窗：之前在 Run 返回处
// 就 signal.Stop，紧随其后的清理阶段再收到信号会被默认处置直接杀进程。
func (p *Proxy) watchShutdownSignal() chan os.Signal {
	p.sigOnce.Do(func() {
		signal.Notify(p.sigChan, shutdownSignals...)
	})
	return p.sigChan
}

// Run starts the proxy relay loops and waits for context cancellation.
// It also installs a signal handler so that SIGINT/SIGTERM trigger a graceful
// shutdown: the relay loops unblock via context cancellation and Run returns,
// letting the caller invoke Close to reclaim resources. Run never closes the
// proxy itself, preserving the Run/Close separation relied on by tests.
func (p *Proxy) Run() error {
	p.relayConnLoop()
	p.relayPacketLoop()

	sig := p.watchShutdownSignal()
	select {
	case <-p.ctx.Done():
	case s := <-sig:
		log.Info("received signal ", s, ", shutting down")
		p.cancel()
	}
	return nil
}

// RunAndClose runs the relay loops, then closes the proxy and reports the error
// from either step. Callers must not drop the Close error: main only exits 0 when
// the option handler returns nil, so a tunnel that failed to close would otherwise
// look like a clean shutdown.
func (p *Proxy) RunAndClose() error {
	return errors.Join(p.Run(), p.Close())
}

// releaseTunnels 关闭出站与全部入站 tunnel，并汇总各自的错误。
// 之前这些错误被无条件丢弃，调用方（option handler → main）无法区分
// 「干净退出」和「有端口/句柄没关掉」，退出码永远是 0。
func (p *Proxy) releaseTunnels() error {
	var errs []error
	if err := p.sink.Close(); err != nil {
		errs = append(errs, common.NewError("failed to close the outbound tunnel").Base(err))
	}
	for _, source := range p.sources {
		if err := source.Close(); err != nil {
			errs = append(errs, common.NewError("failed to close an inbound tunnel").Base(err))
		}
	}
	return errors.Join(errs...)
}

// Close shuts down the proxy gracefully. Both phases are bounded by
// shutdownTimeout so that a wedged peer can never keep the process alive:
// the relay wait gives up first, and the resource release can give up too,
// because every tunnel Server.Close does its own unbounded wg.Wait internally.
// The returned error reports tunnels that failed to close; a plain
// signal-triggered shutdown still returns nil.
func (p *Proxy) Close() error {
	p.cancel()
	defer signal.Stop(p.sigChan)
	// Run 已经消费掉第一次关闭信号；这里丢掉清理开始前积压的信号，
	// 只把等待期间新到达的信号当作「等不及」的二次信号。
	select {
	case <-p.sigChan:
	default:
	}
	if !waitBounded(shutdownTimeout, p.sigChan, "wait for relay loops to finish", p.wg.Wait) {
		// 没等空也继续往下走：关闭 source/sink 会让滞留的读写报错退出，
		// 总比带着未关闭的监听端口和文件句柄永久挂住要好。
		log.Warn("graceful shutdown did not complete; forcing resource release")
	}
	var errs []error
	released := make(chan error, 1) // 带缓冲：后台 goroutine 在等待者放弃后也能写入并退出
	if waitBounded(shutdownTimeout, p.sigChan, "close tunnels", func() { released <- p.releaseTunnels() }) {
		// 只有确认后台跑完了才读结果，超时放弃时它可能仍在运行
		if err := <-released; err != nil {
			errs = append(errs, err)
		}
	}
	if p.logFile != nil {
		if err := p.logFile.Close(); err != nil {
			log.Error(common.NewError("failed to close log file").Base(err))
		}
	}
	return errors.Join(errs...)
}

var (
	defaultBufSize  = 8 * 1024
	defaultBufCount = 1024
)

func (p *Proxy) relayConnLoop() {
	for _, source := range p.sources {
		p.wg.Go(func() {
			for {
				if p.ctx.Err() != nil {
					log.Debug("exiting")
					return
				}
				inbound, err := source.AcceptConn(nil)
				if err != nil {
					log.Error(common.NewError("failed to accept connection").Base(err))
					if p.ctx.Err() != nil {
						return
					}
					continue
				}
				p.wg.Go(func() {
					defer inbound.Close()
					metadata := inbound.Metadata()
					if metadata == nil {
						log.Error("inbound connection has no metadata; check the protocol stack configuration")
						return
					}
					outbound, err := p.sink.DialConn(metadata.Address, nil)
					if err != nil {
						log.Error(common.NewError("proxy failed to dial connection").Base(err))
						return
					}
					defer outbound.Close()

					done := make(chan struct{})
					closeDone := sync.OnceFunc(func() { close(done) })

					p.wg.Go(func() {
						buffer := p.bufPool.Get()
						defer p.bufPool.Put(buffer)
						_, err := io.CopyBuffer(inbound, outbound, buffer)
						if err != nil {
							log.Debug(err)
						}
						closeDone()
					})

					p.wg.Go(func() {
						buffer := p.bufPool.Get()
						defer p.bufPool.Put(buffer)
						_, err := io.CopyBuffer(outbound, inbound, buffer)
						if err != nil {
							log.Debug(err)
						}
						closeDone()
					})

					select {
					case <-done:
						log.Debug("conn relay ends")
					case <-p.ctx.Done():
						log.Debug("shutting down conn relay")
					}
				})
			}
		})
	}
}

func (p *Proxy) relayPacketLoop() {
	for _, source := range p.sources {
		p.wg.Go(func() {
			for {
				if p.ctx.Err() != nil {
					log.Debug("exiting")
					return
				}
				inbound, err := source.AcceptPacket(nil)
				if err != nil {
					log.Error(common.NewError("failed to accept packet").Base(err))
					if p.ctx.Err() != nil {
						return
					}
					continue
				}
				p.wg.Go(func() {
					defer inbound.Close()
					outbound, err := p.sink.DialPacket(nil)
					if err != nil {
						log.Error(common.NewError("proxy failed to dial packet").Base(err))
						return
					}
					defer outbound.Close()

					done := make(chan struct{})
					closeDone := sync.OnceFunc(func() { close(done) })

					p.wg.Go(func() {
						for {
							buf := p.packetPool.Get()
							n, metadata, err := inbound.ReadWithMetadata(buf)
							if err != nil {
								p.packetPool.Put(buf)
								log.Debug(err)
								closeDone()
								return
							}
							if n == 0 {
								p.packetPool.Put(buf)
								closeDone()
								return
							}
							_, err = outbound.WriteWithMetadata(buf[:n], metadata)
							p.packetPool.Put(buf)
							if err != nil {
								log.Debug(err)
								closeDone()
								return
							}
						}
					})

					p.wg.Go(func() {
						for {
							buf := p.packetPool.Get()
							n, metadata, err := outbound.ReadWithMetadata(buf)
							if err != nil {
								p.packetPool.Put(buf)
								log.Debug(err)
								closeDone()
								return
							}
							if n == 0 {
								p.packetPool.Put(buf)
								closeDone()
								return
							}
							_, err = inbound.WriteWithMetadata(buf[:n], metadata)
							p.packetPool.Put(buf)
							if err != nil {
								log.Debug(err)
								closeDone()
								return
							}
						}
					})

					select {
					case <-done:
						log.Debug("packet relay ends")
					case <-p.ctx.Done():
						log.Debug("shutting down packet relay")
					}
				})
			}
		})
	}
}

func NewProxy(ctx context.Context, cancel context.CancelFunc, sources []tunnel.Server, sink tunnel.Client) *Proxy {
	bufSize := defaultBufSize
	bufCount := defaultBufCount
	if cfg, ok := config.FromContext(ctx, Name).(*Config); ok {
		if cfg.RelayBufferSize > 0 {
			bufSize = cfg.RelayBufferSize
		}
		if cfg.RelayBufferCount > 0 {
			bufCount = cfg.RelayBufferCount
		}
	}
	// UDP 包必须整包读进 buffer：tunnel/trojan 的 ReadWithMetadata 在
	// len(payload) < 包长 时直接返回错误，中继会就此断开整条 packet 流。
	// 所以即便把 relay_buffer_size 调小，包路径也要留够 MaxPacketSize；
	// 尺寸够用时复用同一个池，避免常驻内存翻倍。
	bufPool := newBoundedBufPool(bufSize, bufCount)
	packetPool := bufPool
	if bufSize < MaxPacketSize {
		packetPool = newBoundedBufPool(MaxPacketSize, bufCount)
	}
	return &Proxy{
		sources:    sources,
		sink:       sink,
		ctx:        ctx,
		cancel:     cancel,
		bufPool:    bufPool,
		packetPool: packetPool,
		sigChan:    make(chan os.Signal, 1),
	}
}

type Creator func(ctx context.Context) (*Proxy, error)

var (
	creators = make(map[string]Creator)
	mu       sync.RWMutex
)

func RegisterProxyCreator(name string, creator Creator) {
	mu.Lock()
	defer mu.Unlock()
	creators[name] = creator
}

func NewProxyFromConfigData(data []byte, isJSON bool) (*Proxy, error) {
	// 使用 crypto/rand 生成实例 ID，避免 math/rand 的可预测性
	instanceID, err := common.SecureRandInt(math.MaxInt)
	if err != nil {
		return nil, common.NewError("failed to generate secure instance ID").Base(err)
	}
	ctx := context.WithValue(context.Background(), proxyIDKey(Name+"_ID"), instanceID)
	if isJSON {
		ctx, err = config.WithJSONConfig(ctx, data)
		if err != nil {
			return nil, err
		}
	} else {
		ctx, err = config.WithYAMLConfig(ctx, data)
		if err != nil {
			return nil, err
		}
	}
	cfg := config.FromContext(ctx, Name).(*Config)
	mu.RLock()
	create, ok := creators[strings.ToUpper(cfg.RunType)]
	mu.RUnlock()
	if !ok {
		return nil, common.NewError("unknown proxy type: " + cfg.RunType)
	}
	log.SetLogLevel(log.LogLevel(cfg.LogLevel))
	var logFile *os.File
	if cfg.LogFile != "" {
		// 日志文件可能包含敏感信息，权限收紧为 0o600（仅 owner 可读写）
		file, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, common.NewError("failed to open log file").Base(err)
		}
		logFile = file
		log.SetOutput(file)
	}
	p, err := create(ctx)
	if err != nil {
		if logFile != nil {
			if cerr := logFile.Close(); cerr != nil {
				log.Warn(common.NewError("failed to close log file").Base(cerr))
			}
		}
		return nil, err
	}
	p.logFile = logFile
	return p, nil
}
