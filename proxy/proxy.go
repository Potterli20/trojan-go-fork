package proxy

import (
	"context"
	"io"
	"math"

	"os"
	"strings"
	"sync"

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
	sources []tunnel.Server
	sink    tunnel.Client
	ctx     context.Context
	cancel  context.CancelFunc
	bufSize int
	bufPool *boundedBufPool
	wg      sync.WaitGroup
	logFile *os.File
}

// boundedBufPool 带驻留数量上限的转发 buffer 池：
// 池内最多驻留 limit 个 buffer（limit<=0 时不池化），池空或池满时临时分配/直接丢弃，
// 由 GC 回收，从而限制转发层的常驻内存占用。
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

// Run starts the proxy relay loops and waits for context cancellation
func (p *Proxy) Run() error {
	p.relayConnLoop()
	p.relayPacketLoop()
	<-p.ctx.Done()
	return nil
}

// Close shuts down the proxy gracefully
func (p *Proxy) Close() error {
	p.cancel()
	p.wg.Wait()
	p.sink.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	for _, source := range p.sources {
		source.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	}
	if p.logFile != nil {
		p.logFile.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	}
	return nil
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
							buf := p.bufPool.Get()
							n, metadata, err := inbound.ReadWithMetadata(buf)
							if err != nil {
								p.bufPool.Put(buf)
								log.Debug(err)
								closeDone()
								return
							}
							if n == 0 {
								p.bufPool.Put(buf)
								closeDone()
								return
							}
							_, err = outbound.WriteWithMetadata(buf[:n], metadata)
							p.bufPool.Put(buf)
							if err != nil {
								log.Debug(err)
								closeDone()
								return
							}
						}
					})

					p.wg.Go(func() {
						for {
							buf := p.bufPool.Get()
							n, metadata, err := outbound.ReadWithMetadata(buf)
							if err != nil {
								p.bufPool.Put(buf)
								log.Debug(err)
								closeDone()
								return
							}
							if n == 0 {
								p.bufPool.Put(buf)
								closeDone()
								return
							}
							_, err = inbound.WriteWithMetadata(buf[:n], metadata)
							p.bufPool.Put(buf)
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
	return &Proxy{
		sources: sources,
		sink:    sink,
		ctx:     ctx,
		cancel:  cancel,
		bufSize: bufSize,
		bufPool: newBoundedBufPool(bufSize, bufCount),
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
			logFile.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		}
		return nil, err
	}
	p.logFile = logFile
	return p, nil
}
