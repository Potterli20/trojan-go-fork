package proxy

import (
	"context"
	"io"
	"math/rand"
	"net"
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
	wg      sync.WaitGroup
}

func (p *Proxy) Run() error {
	p.relayConnLoop()
	p.relayPacketLoop()
	p.wg.Wait()
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

func (p *Proxy) relayConnLoop() {
	for _, source := range p.sources {
		p.wg.Add(1)
		go func(source tunnel.Server) {
			defer p.wg.Done()
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

				p.wg.Add(1)
				go func(inbound tunnel.Conn) {
					defer p.wg.Done()
					defer inbound.Close()

					// Check if the address is not nil before using it
					if inbound.Metadata().Address == nil {
						log.Error("Address is nil")
						return
					}

					outbound, err := p.sink.DialConn(inbound.Metadata().Address, nil)
					if err != nil {
						log.Error(common.NewError("proxy failed to dial connection").Base(err))
						return
					}
					defer outbound.Close()

					errChan := make(chan error, 2)
					copyConn := func(a, b net.Conn) {
						_, err := io.Copy(a, b)
						errChan <- err
					}

					go copyConn(inbound, outbound)
					go copyConn(outbound, inbound)

					select {
					case err = <-errChan:
						if err != nil {
							log.Error(err)
						}
					case <-p.ctx.Done():
						log.Debug("shutting down conn relay")
						return
					}
					log.Debug("conn relay ends")
				}(inbound)
			}
		}(source)
	}
}

func isUPnPPacket(packet tunnel.PacketConn) bool {
	buf := make([]byte, MaxPacketSize)
	n, _, err := packet.ReadFrom(buf)
	if err != nil {
		log.Error(common.NewError("error reading from packet connection").Base(err))
		return false
	}
	if n < 4 {
		return false
	}
	if string(buf[:4]) == "M-SEARCH * HTTP/" {
		return true
	}
	return false
}

func (p *Proxy) relayPacketLoop() {
	defer p.cancel()
	var wg sync.WaitGroup

	for _, source := range p.sources {
		wg.Add(1)
		go func(source tunnel.Server) {
			defer wg.Done()

			for {
				inbound, err := source.AcceptPacket(nil)
				if err != nil {
					log.Error(common.NewError("failed to accept packet").Base(err))
					return
				}

				// Check if incoming packet is a UPnP packet
				if isUPnPPacket(inbound) {
					inbound.Close()
					log.Error("UPnPPacket Detected!")
					continue
				}

				outbound, err := p.sink.DialPacket(nil)
				if err != nil {
					log.Error(common.NewError("proxy failed to dial packet").Base(err))
					inbound.Close()
					return
				}

				wg.Add(2)
				copyPacket := func(a, b tunnel.PacketConn) {
					defer wg.Done()
					defer a.Close()
					defer b.Close()

					for {
						buf := make([]byte, MaxPacketSize)
						n, metadata, err := a.ReadWithMetadata(buf)
						if err != nil {
							if !strings.Contains(err.Error(), "use of closed network connection") {
								inbound.Close()
								log.Error(err)
							}
							return
						}

						if n == 0 {
							return
						}

						_, err = b.WriteWithMetadata(buf[:n], metadata)
						if err != nil {
							if !strings.Contains(err.Error(), "use of closed network connection") {
								inbound.Close()
								log.Error(err)
							}
							return
						}
					}
				}

				go copyPacket(inbound, outbound)
				go copyPacket(outbound, inbound)
			}
		}(source)
	}

	wg.Wait()
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
