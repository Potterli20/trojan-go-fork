package trojan

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Potterli20/trojan-go-fork/api"
	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/statistic"
	"github.com/Potterli20/trojan-go-fork/statistic/memory"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/Potterli20/trojan-go-fork/tunnel/mux"
)

const (
	MaxPacketSize = 1024 * 8
)

const (
	Connect   tunnel.Command = 1
	Associate tunnel.Command = 3
	Mux       tunnel.Command = 0x7f
)

type OutboundConn struct {
	sent uint64
	recv uint64

	metadata          *tunnel.Metadata
	user              statistic.User
	headerWrittenOnce sync.Once
	ctx               context.Context
	cancel            context.CancelFunc
	net.Conn
}

func (c *OutboundConn) Metadata() *tunnel.Metadata {
	return c.metadata
}

func (c *OutboundConn) WriteHeader(payload []byte) (bool, error) {
	var err error
	written := false
	c.headerWrittenOnce.Do(func() {
		hash := c.user.GetHash()
		buf := bytes.NewBuffer(make([]byte, 0, MaxPacketSize))
		crlf := []byte{0x0d, 0x0a}
		buf.Write([]byte(hash))
		buf.Write(crlf)
		c.metadata.WriteTo(buf)
		buf.Write(crlf)
		if payload != nil {
			buf.Write(payload)
		}
		_, err = c.Conn.Write(buf.Bytes())
		if err == nil {
			written = true
			log.Debug("trojan header written for", c.metadata.Address)
		} else {
			log.Error(common.NewError("failed to write trojan header").Base(err))
		}
	})
	return written, err
}

func (c *OutboundConn) Write(p []byte) (int, error) {
	written, err := c.WriteHeader(p)
	if err != nil {
		return 0, common.NewError("trojan failed to flush header with payload").Base(err)
	}
	if written {
		return len(p), nil
	}
	n, err := c.Conn.Write(p)
	c.user.AddSentTraffic(n)
	atomic.AddUint64(&c.sent, uint64(n))
	return n, err
}

func (c *OutboundConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err != nil && err != io.EOF {
		log.Debug("trojan connection read error:", err)
	}
	c.user.AddRecvTraffic(n)
	atomic.AddUint64(&c.recv, uint64(n))
	return n, err
}

func (c *OutboundConn) Close() error {
	c.cancel()
	log.Info("connection to", c.metadata, "closed", "sent:", common.HumanFriendlyTraffic(atomic.LoadUint64(&c.sent)), "recv:", common.HumanFriendlyTraffic(atomic.LoadUint64(&c.recv)))
	return c.Conn.Close()
}

type Client struct {
	underlay tunnel.Client
	user     statistic.User
	ctx      context.Context
	cancel   context.CancelFunc
}

func (c *Client) Close() error {
	c.cancel()
	log.Debug("trojan client closing")
	return c.underlay.Close()
}

func (c *Client) DialConn(addr *tunnel.Address, overlay tunnel.Tunnel) (tunnel.Conn, error) {
	log.Debug("dialing trojan connection to", addr)
	conn, err := c.underlay.DialConn(addr, &Tunnel{})
	if err != nil {
		log.Error(common.NewError("failed to dial underlying connection").Base(err))
		return nil, err
	}
	ctx, cancel := context.WithCancel(c.ctx)
	newConn := &OutboundConn{
		Conn:   conn,
		user:   c.user,
		ctx:    ctx,
		cancel: cancel,
		metadata: &tunnel.Metadata{
			Command: Connect,
			Address: addr,
		},
	}
	if _, ok := overlay.(*mux.Tunnel); ok {
		newConn.metadata.Command = Mux
		log.Debug("mux connection requested")
	}

	go func(newConn *OutboundConn) {
		select {
		case <-time.After(time.Millisecond * 100):
			newConn.WriteHeader(nil)
		case <-newConn.ctx.Done():
			log.Debug("connection closed before header flush")
			return
		}
	}(newConn)
	return newConn, nil
}

func (c *Client) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	log.Debug("dialing trojan packet connection")
	fakeAddr := &tunnel.Address{
		DomainName:  "UDP_CONN",
		AddressType: tunnel.DomainName,
	}

	conn, err := c.underlay.DialConn(fakeAddr, &Tunnel{})
	if err != nil {
		log.Error(common.NewError("failed to dial underlying connection for UDP").Base(err))
		return nil, err
	}
	return &PacketConn{
		Conn: &OutboundConn{
			Conn: conn,
			user: c.user,
			metadata: &tunnel.Metadata{
				Command: Associate,
				Address: fakeAddr,
			},
		},
	}, nil
}

func NewClient(ctx context.Context, client tunnel.Client) (*Client, error) {
	ctx, cancel := context.WithCancel(ctx)
	auth, err := statistic.NewAuthenticator(ctx, memory.Name)
	if err != nil {
		cancel()
		return nil, common.NewError("failed to create authenticator").Base(err)
	}

	cfg := config.FromContext(ctx, Name).(*Config)
	if cfg.API.Enabled {
		log.Info("starting API service for client")
		go api.RunService(ctx, Name+"_CLIENT", auth)
	}

	var user statistic.User
	for _, u := range auth.ListUsers() {
		user = u
		break
	}
	if user == nil {
		cancel()
		return nil, common.NewError("no valid user found")
	}

	log.Debug("trojan client created")
	return &Client{
		underlay: client,
		ctx:      ctx,
		user:     user,
		cancel:   cancel,
	}, nil
}
