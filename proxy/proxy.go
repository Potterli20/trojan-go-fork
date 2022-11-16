package proxy

import (
	"context"
	"io"
	"math/rand"
	"time"

	//"net"
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

// Proxy relay connections and packets
type Proxy struct {
	sources []tunnel.Server
	sink    tunnel.Client
	ctx     context.Context
	cancel  context.CancelFunc
}

func (p *Proxy) Run() error {
	p.relayConnLoop()
	p.relayPacketLoop()
	<-p.ctx.Done()
	return nil
}

func (p *Proxy) Close() error {
	p.cancel()
	p.sink.Close()
	for _, source := range p.sources {
		source.Close()
	}
	return nil
}

var bufPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 8*1024, 8*1024)
	},
}

func (p *Proxy) relayConnLoop() {
	copyConn := func(dst io.Writer, src io.Reader, errChan chan error) {
		buffer := bufPool.Get().([]byte)
		_, err := io.CopyBuffer(dst, src, buffer)
		bufPool.Put(buffer)
		errChan <- err
	}

	for _, source := range p.sources {
		go func(source tunnel.Server) {
			for {
				inbound, err := source.AcceptConn(nil)
				if err != nil {
					select {
					case <-p.ctx.Done():
						log.Debug("exiting")
						return
					default:
					}
					log.Error(common.NewError("failed to accept connection").Base(err))
					continue
				}
				go func(inbound tunnel.Conn) {
					defer inbound.Close()
					outbound, err := p.sink.DialConn(inbound.Metadata().Address, nil)
					if err != nil {
						log.Error(common.NewError("proxy failed to dial connection").Base(err))
						return
					}
					defer outbound.Close()
					errChan := make(chan error, 2)

					go copyConn(inbound, outbound, errChan)
					time.Sleep(time.Millisecond * 10)
					go copyConn(outbound, inbound, errChan)
					select {
					case err = <-errChan:
						if err != nil {
							log.Error(err)
						}
					case <-p.ctx.Done():
						log.Debug("shutting down conn relay")
						return
					//Exit goroutine when Timeout, avoid goroutine leakage.
					case <-time.After(time.Duration(time.Second * 30)):
						log.Debug("timeout conn relay")
						return

					}
					log.Debug("conn relay ends")
				}(inbound)
			}
		}(source)
	}
}

func (p *Proxy) relayPacketLoop() {

	copyPacket := func(a, b tunnel.PacketConn, errChan chan error) {
		for {
			//buf := make([]byte, MaxPacketSize)
			buf := bufPool.Get().([]byte)
			defer bufPool.Put(buf)

			n, metadata, err := a.ReadWithMetadata(buf)
			if err != nil {
				errChan <- err
				return
			}
			if n == 0 {
				errChan <- nil
				return
			}
			_, err = b.WriteWithMetadata(buf[:n], metadata)
			if err != nil {
				errChan <- err
				return
			}
		}
	}

	for _, source := range p.sources {
		go func(source tunnel.Server) {
			for {
				inbound, err := source.AcceptPacket(nil)
				if err != nil {
					select {
					case <-p.ctx.Done():
						log.Debug("exiting")
						return
					default:
					}
					log.Error(common.NewError("failed to accept packet").Base(err))
					continue
				}
				go func(inbound tunnel.PacketConn) {
					defer inbound.Close()
					outbound, err := p.sink.DialPacket(nil)
					if err != nil {
						log.Error(common.NewError("proxy failed to dial packet").Base(err))
						return
					}
					defer outbound.Close()
					errChan := make(chan error, 2)

					go copyPacket(inbound, outbound, errChan)
					time.Sleep(time.Millisecond * 10)
					go copyPacket(outbound, inbound, errChan)
					select {
					case err = <-errChan:
						if err != nil {
							log.Error(err)
						}
					case <-p.ctx.Done():
						log.Debug("shutting down packet relay")
					//Exit goroutine when Timeout, avoid goroutine leakage.
					case <-time.After(time.Duration(time.Second * 30)):
						log.Debug("timeout packet relay")
						return
					}

					log.Debug("packet relay ends")
				}(inbound)
			}
		}(source)
	}
}

func NewProxy(ctx context.Context, cancel context.CancelFunc, sources []tunnel.Server, sink tunnel.Client) *Proxy {
	return &Proxy{
		sources: sources,
		sink:    sink,
		ctx:     ctx,
		cancel:  cancel,
	}
}

type Creator func(ctx context.Context) (*Proxy, error)

var creators = make(map[string]Creator)

func RegisterProxyCreator(name string, creator Creator) {
	creators[name] = creator
}

func NewProxyFromConfigData(data []byte, isJSON bool) (*Proxy, error) {
	// create a unique context for each proxy instance to avoid duplicated authenticator
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
