package proxy

import (
	"context"
	"io"
	"math/rand/v2"
	"time"

	"os"
	"strings"
	"sync"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

const Name = "PROXY"

const (
	MaxPacketSize = 1024 * 8
)

type Proxy struct {
	sources []tunnel.Server
	sink    tunnel.Client
	ctx     context.Context
	cancel  context.CancelFunc
	bufSize int
	bufPool sync.Pool
	wg      sync.WaitGroup
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
	p.sink.Close()
	for _, source := range p.sources {
		source.Close()
	}
	return nil
}

var defaultBufSize = 8 * 1024

func (p *Proxy) relayConnLoop() {
	for _, source := range p.sources {
		p.wg.Add(1)
		go func(source tunnel.Server) {
			defer p.wg.Done()
			for {
				select {
				case <-p.ctx.Done():
					log.Debug("exiting")
					return
				default:
				}
				inbound, err := source.AcceptConn(nil)
				if err != nil {
					log.Error(common.NewError("failed to accept connection").Base(err))
					continue
				}
				p.wg.Add(1)
				go func(inbound tunnel.Conn) {
					defer p.wg.Done()
					defer inbound.Close()
					outbound, err := p.sink.DialConn(inbound.Metadata().Address, nil)
					if err != nil {
						log.Error(common.NewError("proxy failed to dial connection").Base(err))
						return
					}
					defer outbound.Close()

					done := make(chan struct{})
					var once sync.Once
					closeDone := func() { once.Do(func() { close(done) }) }

					go func() {
						buffer := p.bufPool.Get().([]byte)
						defer p.bufPool.Put(buffer)
						_, err := io.CopyBuffer(inbound, outbound, buffer)
						if err != nil {
							log.Debug(err)
						}
						closeDone()
					}()

					go func() {
						buffer := p.bufPool.Get().([]byte)
						defer p.bufPool.Put(buffer)
						_, err := io.CopyBuffer(outbound, inbound, buffer)
						if err != nil {
							log.Debug(err)
						}
						closeDone()
					}()

					select {
					case <-done:
						log.Debug("conn relay ends")
					case <-p.ctx.Done():
						log.Debug("shutting down conn relay")
					case <-time.After(time.Second * 30):
						log.Debug("timeout conn relay")
					}
				}(inbound)
			}
		}(source)
	}
}

func (p *Proxy) relayPacketLoop() {
	for _, source := range p.sources {
		p.wg.Add(1)
		go func(source tunnel.Server) {
			defer p.wg.Done()
			for {
				select {
				case <-p.ctx.Done():
					log.Debug("exiting")
					return
				default:
				}
				inbound, err := source.AcceptPacket(nil)
				if err != nil {
					log.Error(common.NewError("failed to accept packet").Base(err))
					continue
				}
				p.wg.Add(1)
				go func(inbound tunnel.PacketConn) {
					defer p.wg.Done()
					defer inbound.Close()
					outbound, err := p.sink.DialPacket(nil)
					if err != nil {
						log.Error(common.NewError("proxy failed to dial packet").Base(err))
						return
					}
					defer outbound.Close()

					done := make(chan struct{})
					var once sync.Once
					closeDone := func() { once.Do(func() { close(done) }) }

					go func() {
						for {
							buf := p.bufPool.Get().([]byte)
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
					}()

					go func() {
						for {
							buf := p.bufPool.Get().([]byte)
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
					}()

					select {
					case <-done:
						log.Debug("packet relay ends")
					case <-p.ctx.Done():
						log.Debug("shutting down packet relay")
					case <-time.After(time.Second * 30):
						log.Debug("timeout packet relay")
					}
				}(inbound)
			}
		}(source)
	}
}

func NewProxy(ctx context.Context, cancel context.CancelFunc, sources []tunnel.Server, sink tunnel.Client) *Proxy {
	bufSize := defaultBufSize
	if cfg, ok := config.FromContext(ctx, Name).(*Config); ok {
		if cfg.RelayBufferSize > 0 {
			bufSize = cfg.RelayBufferSize
		}
	}
	return &Proxy{
		sources: sources,
		sink:    sink,
		ctx:     ctx,
		cancel:  cancel,
		bufSize: bufSize,
		bufPool: sync.Pool{
			New: func() any {
				return make([]byte, bufSize)
			},
		},
	}
}

type Creator func(ctx context.Context) (*Proxy, error)

var creators = make(map[string]Creator)

func RegisterProxyCreator(name string, creator Creator) {
	creators[name] = creator
}

func NewProxyFromConfigData(data []byte, isJSON bool) (*Proxy, error) {
	ctx := context.WithValue(context.Background(), Name+"_ID", rand.Int())
	var err error
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
	create, ok := creators[strings.ToUpper(cfg.RunType)]
	if !ok {
		return nil, common.NewError("unknown proxy type: " + cfg.RunType)
	}
	log.SetLogLevel(log.LogLevel(cfg.LogLevel))
	if cfg.LogFile != "" {
		file, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, common.NewError("failed to open log file").Base(err)
		}
		log.SetOutput(file)
	}
	return create(ctx)
}

// Stats returns proxy statistics for monitoring
type Stats struct {
	ActiveConnections int64
	ActivePackets     int64
	PoolHits          int64
	PoolMisses        int64
	TotalRelays       int64
}

var stats Stats
