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
			if log.ShouldLog(log.DebugLevel) {
				log.Debug("[Trojan] Header written for", c.metadata.Address)
				log.Debug("[Trojan] Target:", c.metadata.Command, c.metadata.Address)
			}
		} else {
			log.Error("[Trojan] Failed to write header:", err)
		}
	})
	return written, err
}

func (c *OutboundConn) Write(p []byte) (int, error) {
	written, err := c.WriteHeader(p)
	if err != nil {
		log.Error("[Trojan] Failed to flush header with payload:", err)
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
	if err != nil && err != io.EOF && log.ShouldLog(log.DebugLevel) {
		log.Debug("[Trojan] Connection read error:", err)
	}
	c.user.AddRecvTraffic(n)
	atomic.AddUint64(&c.recv, uint64(n))
	return n, err
}

func (c *OutboundConn) Close() error {
	c.cancel()
	if log.ShouldLog(log.InfoLevel) {
		log.Info("[Trojan] Connection to", c.metadata, "closed", "sent:", common.HumanFriendlyTraffic(atomic.LoadUint64(&c.sent)), "recv:", common.HumanFriendlyTraffic(atomic.LoadUint64(&c.recv)))
	}
	if err := c.Conn.Close(); err != nil {
		log.Error("[Trojan] Failed to close connection:", err)
		return err
	}
	return nil
}

type Client struct {
	underlay tunnel.Client
	user     statistic.User
	ctx      context.Context
	cancel   context.CancelFunc
}

func (c *Client) Close() error {
	log.Info("[Trojan] Closing client")
	c.cancel()
	if err := c.underlay.Close(); err != nil {
		log.Error("[Trojan] Failed to close underlay:", err)
		return err
	}
	log.Info("[Trojan] Client closed successfully")
	return nil
}

func (c *Client) DialConn(addr *tunnel.Address, overlay tunnel.Tunnel) (tunnel.Conn, error) {
	if log.ShouldLog(log.DebugLevel) {
		log.Debug("[Trojan] DialConn start - target:", addr, "user:", c.user.GetHash())
	}

	isMux := false
	if _, ok := overlay.(*mux.Tunnel); ok {
		isMux = true
	}

	tracker := log.NewConnectionTracker("Trojan", "DialConn").
		WithField("target", addr.String()).
		WithField("user", c.user.GetHash()).
		WithField("mux", isMux)

	conn, err := c.underlay.DialConn(addr, &Tunnel{})
	if err != nil {
		tracker.Error(err)
		return nil, common.NewError("failed to dial underlying connection").Base(err)
	}
	tracker.Success()

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

	if isMux {
		newConn.metadata.Command = Mux
	}

	go func(newConn *OutboundConn) {
		select {
		case <-time.After(time.Millisecond * 100):
			newConn.WriteHeader(nil)
		case <-newConn.ctx.Done():
			return
		}
	}(newConn)

	return newConn, nil
}

func (c *Client) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	if log.ShouldLog(log.DebugLevel) {
		log.Debug("[Trojan] DialPacket start")
	}

	fakeAddr := &tunnel.Address{
		DomainName:  "UDP_CONN",
		AddressType: tunnel.DomainName,
	}

	tracker := log.NewConnectionTracker("Trojan", "DialPacket").
		WithField("target", fakeAddr.String()).
		WithField("user", c.user.GetHash())

	conn, err := c.underlay.DialConn(fakeAddr, &Tunnel{})
	if err != nil {
		tracker.Error(err)
		return nil, common.NewError("failed to dial underlying connection for UDP").Base(err)
	}
	tracker.Success()

	ctx, cancel := context.WithCancel(c.ctx)

	return &PacketConn{
		Conn: &OutboundConn{
			Conn:   conn,
			user:   c.user,
			ctx:    ctx,
			cancel: cancel,
			metadata: &tunnel.Metadata{
				Command: Associate,
				Address: fakeAddr,
			},
		},
	}, nil
}

func NewClient(ctx context.Context, client tunnel.Client) (*Client, error) {
	log.Info("[Trojan] Creating client")

	ctx, cancel := context.WithCancel(ctx)
	if log.ShouldLog(log.DebugLevel) {
		log.Debug("[Trojan] Creating authenticator...")
	}
	auth, err := statistic.NewAuthenticator(ctx, memory.Name)
	if err != nil {
		cancel()
		log.Error("[Trojan] Failed to create authenticator:", err)
		return nil, common.NewError("failed to create authenticator").Base(err)
	}
	log.Info("[Trojan] Authenticator created successfully")

	cfg := config.FromContext(ctx, Name).(*Config)
	if log.ShouldLog(log.DebugLevel) {
		log.Debug("[Trojan] RemoteHost:", cfg.RemoteHost)
		log.Debug("[Trojan] RemotePort:", cfg.RemotePort)
		log.Debug("[Trojan] API Enabled:", cfg.API.Enabled)
	}

	if cfg.API.Enabled {
		log.Info("[Trojan] Starting API service")
		go api.RunService(ctx, Name+"_CLIENT", auth)
	}

	var user statistic.User
	userList := auth.ListUsers()
	if log.ShouldLog(log.DebugLevel) {
		log.Debug("[Trojan] Number of users configured:", len(userList))
		for i, u := range userList {
			log.Debug("[Trojan] User", i+1, "hash:", u.GetHash())
		}
	}

	if len(userList) == 0 {
		cancel()
		log.Error("[Trojan] No valid user found in configuration")
		return nil, common.NewError("no valid user found")
	}
	user = userList[0]

	log.Info("[Trojan] Using user hash:", user.GetHash())
	log.Info("[Trojan] Client created successfully")

	return &Client{
		underlay: client,
		ctx:      ctx,
		user:     user,
		cancel:   cancel,
	}, nil
}
