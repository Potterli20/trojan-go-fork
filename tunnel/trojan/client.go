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
			log.Debug("[Trojan Client] Trojan header written successfully for", c.metadata.Address)
			log.Debug("[Trojan Client] Target:", c.metadata.Command, c.metadata.Address)
		} else {
			log.Error("[Trojan Client] Failed to write trojan header:", err)
		}
	})
	return written, err
}

func (c *OutboundConn) Write(p []byte) (int, error) {
	written, err := c.WriteHeader(p)
	if err != nil {
		log.Error("[Trojan Client] Failed to flush header with payload:", err)
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
		log.Debug("[Trojan Client] Connection read error:", err)
	}
	c.user.AddRecvTraffic(n)
	atomic.AddUint64(&c.recv, uint64(n))
	return n, err
}

func (c *OutboundConn) Close() error {
	c.cancel()
	log.Info("[Trojan Client] Connection to", c.metadata, "closed", "sent:", common.HumanFriendlyTraffic(atomic.LoadUint64(&c.sent)), "recv:", common.HumanFriendlyTraffic(atomic.LoadUint64(&c.recv)))
	if err := c.Conn.Close(); err != nil {
		log.Error("[Trojan Client] Failed to close connection:", err)
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
	log.Info("[Trojan Client] Closing Trojan client")
	c.cancel()
	if err := c.underlay.Close(); err != nil {
		log.Error("[Trojan Client] Failed to close underlay:", err)
		return err
	}
	log.Info("[Trojan Client] Trojan client closed successfully")
	return nil
}

func (c *Client) DialConn(addr *tunnel.Address, overlay tunnel.Tunnel) (tunnel.Conn, error) {
	log.Debug("[Trojan Client] ========== Trojan DialConn Start ==========")
	log.Debug("[Trojan Client] Target address:", addr)
	log.Debug("[Trojan Client] User hash:", c.user.GetHash())

	isMux := false
	if _, ok := overlay.(*mux.Tunnel); ok {
		isMux = true
		log.Debug("[Trojan Client] Mux mode: enabled")
	} else {
		log.Debug("[Trojan Client] Mux mode: disabled")
	}

	log.Debug("[Trojan Client] Step 1: Dialing underlying connection...")
	startTime := time.Now()
	conn, err := c.underlay.DialConn(addr, &Tunnel{})
	dialDuration := time.Since(startTime)
	if err != nil {
		log.Error("[Trojan Client] Failed to dial underlying connection after", dialDuration, ":", err)
		return nil, common.NewError("failed to dial underlying connection").Base(err)
	}
	log.Info("[Trojan Client] Underlying connection established in", dialDuration)

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
		log.Debug("[Trojan Client] Command set to: Mux (0x7f)")
	} else {
		log.Debug("[Trojan Client] Command set to: Connect (1)")
	}

	log.Debug("[Trojan Client] Step 2: Starting delayed header write goroutine...")
	go func(newConn *OutboundConn) {
		select {
		case <-time.After(time.Millisecond * 100):
			log.Debug("[Trojan Client] Delayed header write triggered")
			newConn.WriteHeader(nil)
		case <-newConn.ctx.Done():
			log.Debug("[Trojan Client] Connection closed before delayed header write")
			return
		}
	}(newConn)

	log.Debug("[Trojan Client] ========== Trojan DialConn End ==========")
	return newConn, nil
}

func (c *Client) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	log.Debug("[Trojan Client] ========== Trojan DialPacket Start ==========")
	log.Debug("[Trojan Client] Dialing UDP connection via Trojan")

	fakeAddr := &tunnel.Address{
		DomainName:  "UDP_CONN",
		AddressType: tunnel.DomainName,
	}

	startTime := time.Now()
	conn, err := c.underlay.DialConn(fakeAddr, &Tunnel{})
	dialDuration := time.Since(startTime)
	if err != nil {
		log.Error("[Trojan Client] Failed to dial underlying connection for UDP after", dialDuration, ":", err)
		return nil, common.NewError("failed to dial underlying connection for UDP").Base(err)
	}
	log.Info("[Trojan Client] UDP underlying connection established in", dialDuration)

	ctx, cancel := context.WithCancel(c.ctx)

	log.Debug("[Trojan Client] ========== Trojan DialPacket End ==========")
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
	log.Info("[Trojan Client] ========== Creating Trojan Client ==========")

	ctx, cancel := context.WithCancel(ctx)
	log.Debug("[Trojan Client] Creating authenticator...")
	auth, err := statistic.NewAuthenticator(ctx, memory.Name)
	if err != nil {
		cancel()
		log.Error("[Trojan Client] Failed to create authenticator:", err)
		return nil, common.NewError("failed to create authenticator").Base(err)
	}
	log.Info("[Trojan Client] Authenticator created successfully")

	cfg := config.FromContext(ctx, Name).(*Config)
	log.Debug("[Trojan Client] RemoteHost:", cfg.RemoteHost)
	log.Debug("[Trojan Client] RemotePort:", cfg.RemotePort)
	log.Debug("[Trojan Client] API Enabled:", cfg.API.Enabled)

	if cfg.API.Enabled {
		log.Info("[Trojan Client] Starting API service")
		go api.RunService(ctx, Name+"_CLIENT", auth)
	}

	var user statistic.User
	userList := auth.ListUsers()
	log.Debug("[Trojan Client] Number of users configured:", len(userList))

	for i, u := range userList {
		log.Debug("[Trojan Client] User", i+1, "hash:", u.GetHash())
	}

	if len(userList) == 0 {
		cancel()
		log.Error("[Trojan Client] No valid user found in configuration")
		return nil, common.NewError("no valid user found")
	}
	user = userList[0]

	log.Info("[Trojan Client] Using user hash:", user.GetHash())
	log.Info("[Trojan Client] ========== Trojan Client Created Successfully ==========")

	return &Client{
		underlay: client,
		ctx:      ctx,
		user:     user,
		cancel:   cancel,
	}, nil
}
