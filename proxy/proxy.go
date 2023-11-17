package proxy

import (
	"context"
	"io"
	"net"
	"sync"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/gorilla/websocket"
)

// WebSocketClientCreator 是 WebSocket 客户端创建函数的接口
type WebSocketClientCreator interface {
	CreateWebSocketClient(address string) (*websocket.Conn, error)
}

// DefaultWebSocketClientCreator 是默认的 WebSocket 客户端创建函数
type DefaultWebSocketClientCreator struct{}

// CreateWebSocketClient 实现 WebSocketClientCreator 接口
func (dwc *DefaultWebSocketClientCreator) CreateWebSocketClient(address string) (*websocket.Conn, error) {
	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(address, nil)
	return conn, err
}

// Proxy 是一个代理类型，负责中继连接和数据包
type Proxy struct {
	sources   []tunnel.Server
	sink      tunnel.Client
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	wsCreator WebSocketClientCreator
}

// NewProxy 创建一个新的 Proxy 实例
func NewProxy(ctx context.Context, cancel context.CancelFunc, sources []tunnel.Server, sink tunnel.Client, wsCreator WebSocketClientCreator) *Proxy {
	return &Proxy{
		sources:   sources,
		sink:      sink,
		ctx:       ctx,
		cancel:    cancel,
		wsCreator: wsCreator,
	}
}

// Run 启动 Proxy，开始中继连接和数据包
func (p *Proxy) Run() error {
	p.relayConnLoop()
	p.relayPacketLoop()
	p.wg.Wait()
	return nil
}

// Close 关闭 Proxy，释放资源
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

					if inbound.Metadata().Address == nil {
						log.Error("Address is nil")
						return
					}

					log.Debug("Dialing connection to address:", inbound.Metadata().Address)

					wsClient, err := p.wsCreator.CreateWebSocketClient(inbound.Metadata().Address.String())
					if err != nil {
						log.Error(common.NewError("failed to create WebSocket client").Base(err))
						return
					}
					defer wsClient.Close()

					errChan := make(chan error, 2)
					copyConn := func(a, b net.Conn) {
						_, err := io.Copy(a, b)
						errChan <- err
					}

					go copyConn(inbound, wsClient)
					go copyConn(wsClient, inbound)

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
