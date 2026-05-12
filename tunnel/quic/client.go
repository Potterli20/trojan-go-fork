package quic

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/apernet/quic-go"
)

type Client struct {
	underlay       tunnel.Client
	remoteAddr     *tunnel.Address
	sni            string
	quicConfig     *quic.Config
	tlsConfig      *tls.Config
	maxIdleTimeout time.Duration
	quicConn       any
	quicConnMutex  sync.RWMutex
	ctx            context.Context
	cancel         context.CancelFunc
}

func (c *Client) Close() error {
	c.cancel()
	c.quicConnMutex.Lock()
	if c.quicConn != nil {
		c.quicConn.(interface {
			CloseWithError(code uint32, reason string) error
		}).CloseWithError(0, "client closed")
		c.quicConn = nil
	}
	c.quicConnMutex.Unlock()
	if c.underlay != nil {
		return c.underlay.Close()
	}
	return nil
}

func (c *Client) getOrCreateConnection() (any, error) {
	c.quicConnMutex.RLock()
	conn := c.quicConn
	c.quicConnMutex.RUnlock()

	if conn != nil {
		return conn, nil
	}

	c.quicConnMutex.Lock()
	defer c.quicConnMutex.Unlock()

	if c.quicConn != nil {
		return c.quicConn, nil
	}

	addrStr := c.remoteAddr.String()
	log.Debug("QUIC dialing to", addrStr)

	conn, err := quic.DialAddr(context.Background(), addrStr, c.tlsConfig, c.quicConfig)
	if err != nil {
		return nil, common.NewError("QUIC failed to dial").Base(err)
	}

	c.quicConn = conn

	go c.keepAliveLoop()

	return conn, nil
}

func (c *Client) keepAliveLoop() {
	ticker := time.NewTicker(time.Second * time.Duration(10))
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.quicConnMutex.RLock()
			conn := c.quicConn
			c.quicConnMutex.RUnlock()
			if conn != nil {
				conn.(interface{ SendMessage([]byte) error }).SendMessage([]byte{})
			}
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Client) DialPacket(tun tunnel.Tunnel) (tunnel.PacketConn, error) {
	conn, err := c.getOrCreateConnection()
	if err != nil {
		return nil, err
	}

	log.Debug("QUIC packet connection created")
	return &PacketConn{conn: conn}, nil
}

func (c *Client) DialConn(address *tunnel.Address, tun tunnel.Tunnel) (tunnel.Conn, error) {
	conn, err := c.getOrCreateConnection()
	if err != nil {
		return nil, err
	}

	stream, err := conn.(interface{ OpenStream() (quic.Stream, error) }).OpenStream()
	if err != nil {
		log.Error(common.NewError("QUIC failed to open stream").Base(err))
		c.quicConnMutex.Lock()
		c.quicConn = nil
		c.quicConnMutex.Unlock()
		return nil, common.NewError("QUIC failed to open stream").Base(err)
	}

	log.Debug("QUIC stream created")
	return &StreamConn{Stream: &stream, conn: conn}, nil
}

type StreamConn struct {
	Stream *quic.Stream
	conn   any
}

func (c *StreamConn) Metadata() *tunnel.Metadata {
	return &tunnel.Metadata{}
}

func (c *StreamConn) LocalAddr() net.Addr {
	return c.conn.(interface{ LocalAddr() net.Addr }).LocalAddr()
}

func (c *StreamConn) RemoteAddr() net.Addr {
	return c.conn.(interface{ RemoteAddr() net.Addr }).RemoteAddr()
}

func (c *StreamConn) Read(p []byte) (int, error) {
	return (*c.Stream).Read(p)
}

func (c *StreamConn) Write(p []byte) (int, error) {
	return (*c.Stream).Write(p)
}

func (c *StreamConn) Close() error {
	return (*c.Stream).Close()
}

func (c *StreamConn) SetDeadline(t time.Time) error {
	return (*c.Stream).SetDeadline(t)
}

func (c *StreamConn) SetReadDeadline(t time.Time) error {
	return (*c.Stream).SetReadDeadline(t)
}

func (c *StreamConn) SetWriteDeadline(t time.Time) error {
	return (*c.Stream).SetWriteDeadline(t)
}

type PacketConn struct {
	conn any
}

func (c *PacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	return c.conn.(interface{ SendMessage([]byte) (int, error) }).SendMessage(p)
}

func (c *PacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, err := c.conn.(interface {
		ReceiveMessage(context.Context, []byte) (int, error)
	}).ReceiveMessage(context.Background(), p)
	if err != nil {
		return 0, nil, err
	}
	return n, c.conn.(interface{ RemoteAddr() net.Addr }).RemoteAddr(), nil
}

func (c *PacketConn) WriteWithMetadata(p []byte, m *tunnel.Metadata) (int, error) {
	return c.conn.(interface{ SendMessage([]byte) (int, error) }).SendMessage(p)
}

func (c *PacketConn) ReadWithMetadata(p []byte) (int, *tunnel.Metadata, error) {
	n, err := c.conn.(interface {
		ReceiveMessage(context.Context, []byte) (int, error)
	}).ReceiveMessage(context.Background(), p)
	if err != nil {
		return 0, nil, err
	}
	return n, &tunnel.Metadata{}, nil
}

func (c *PacketConn) Close() error {
	return nil
}

func (c *PacketConn) LocalAddr() net.Addr {
	return c.conn.(interface{ LocalAddr() net.Addr }).LocalAddr()
}

func (c *PacketConn) RemoteAddr() net.Addr {
	return c.conn.(interface{ RemoteAddr() net.Addr }).RemoteAddr()
}

func (c *PacketConn) SetDeadline(t time.Time) error {
	return nil
}

func (c *PacketConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (c *PacketConn) SetWriteDeadline(t time.Time) error {
	return nil
}

func NewClient(ctx context.Context, underlay tunnel.Client) (*Client, error) {
	cfg := config.FromContext(ctx, Name).(*Config)

	if cfg.RemoteHost == "" {
		return nil, common.NewError("QUIC remote address is empty")
	}

	if cfg.QUIC.ALPN == "" {
		cfg.QUIC.ALPN = "hq-29"
	}

	remoteAddr := tunnel.NewAddressFromHostPort("udp", cfg.RemoteHost, cfg.RemotePort)

	tlsConfig := &tls.Config{
		ServerName:         cfg.RemoteHost,
		InsecureSkipVerify: cfg.QUIC.Insecure,
		NextProtos:         []string{cfg.QUIC.ALPN},
	}

	quicConfig := &quic.Config{
		MaxIdleTimeout:     time.Second * time.Duration(cfg.QUIC.MaxIdleTimeout),
		MaxIncomingStreams: int64(cfg.QUIC.MaxIncomingStreams),
	}

	log.Debug("QUIC client created with ALPN:", cfg.QUIC.ALPN)
	ctx, cancel := context.WithCancel(ctx)
	return &Client{
		underlay:       underlay,
		remoteAddr:     remoteAddr,
		sni:            cfg.RemoteHost,
		tlsConfig:      tlsConfig,
		quicConfig:     quicConfig,
		maxIdleTimeout: time.Second * time.Duration(cfg.QUIC.MaxIdleTimeout),
		ctx:            ctx,
		cancel:         cancel,
	}, nil
}
