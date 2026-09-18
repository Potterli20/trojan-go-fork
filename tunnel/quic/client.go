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
	tlstunnel "github.com/Potterli20/trojan-go-fork/tunnel/tls"
	"github.com/Potterli20/trojan-go-fork/tunnel/tls/fingerprint"
	"github.com/apernet/quic-go"
)

type Client struct {
	underlay       tunnel.Client
	remoteAddr     *tunnel.Address
	sni            string
	quicConfig     *quic.Config
	tlsConfig      *tls.Config
	maxIdleTimeout time.Duration
	congestion     string
	brutalUp       uint64
	brutalDown     uint64
	// quicConn 用具体类型 *quic.Conn 而非 any:此前用 any + 结构体接口断言
	// 绕过编译,却与被 pin 的 quic-go v0.62.1 API 全面不匹配(返回 *Stream 而非
	// Stream、ReceiveDatagram(ctx) 返回 []byte、CloseWithError 收 ApplicationErrorCode),
	// 一旦接入即 panic。改成具体类型后编译器会真正校验这些调用。
	quicConn      *quic.Conn
	quicConnMutex sync.RWMutex
	keepAliveOnce sync.Once
	wg            sync.WaitGroup
	ctx           context.Context
	cancel        context.CancelFunc
}

func (c *Client) Close() error {
	c.cancel()
	c.wg.Wait()
	c.quicConnMutex.Lock()
	if c.quicConn != nil {
		c.quicConn.CloseWithError(quic.ApplicationErrorCode(0), "client closed") //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		c.quicConn = nil
	}
	c.quicConnMutex.Unlock()
	if c.underlay != nil {
		return c.underlay.Close()
	}
	return nil
}

func (c *Client) applyCongestionControl(conn *quic.Conn) {
	ApplyCongestionControl(conn, CongestionConfig{
		Algorithm:  c.congestion,
		BrutalUp:   c.brutalUp,
		BrutalDown: c.brutalDown,
	}, "client")
}

func (c *Client) getOrCreateConnection() (*quic.Conn, error) {
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

	tracker := log.NewConnectionTracker("QUIC", "DialConn").
		WithField("remote_addr", addrStr).
		WithField("congestion", c.congestion)

	log.Debugf("[QUIC] [conn=%s] Dialing to %s with congestion=%s, alpn=%v",
		tracker.ConnID(), addrStr, c.congestion, c.tlsConfig.NextProtos)

	// 用 c.ctx 而非 context.Background():否则 Client.Close() 无法中断在途握手,
	// wg.Wait() 期间握完的连接会无人关闭(泄漏 1 个 QUIC 连接及其内部 goroutine)。
	quicConn, err := quic.DialAddr(c.ctx, addrStr, c.tlsConfig, c.quicConfig)
	if err != nil {
		_ = tracker.Error(err)
		return nil, common.NewError("QUIC failed to dial").Base(err)
	}
	_ = tracker.Success()

	log.Debugf("[QUIC] [conn=%s] Connection established to %s, congestion=%s",
		tracker.ConnID(), addrStr, c.congestion)

	c.applyCongestionControl(quicConn)

	c.quicConn = quicConn

	c.keepAliveOnce.Do(func() {
		c.wg.Go(func() {
			c.keepAliveLoop()
		})
	})

	return quicConn, nil
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
				conn.SendDatagram([]byte{}) //gosec:disable -- keepalive padding,错误忽略：非关键路径
			}
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Client) DialPacket(tun tunnel.Tunnel) (tunnel.PacketConn, error) {
	tracker := log.NewConnectionTracker("QUIC", "DialPacket").
		WithField("remote_addr", c.remoteAddr.String())

	log.Debugf("[QUIC] [conn=%s] Creating packet connection to %s",
		tracker.ConnID(), c.remoteAddr.String())

	conn, err := c.getOrCreateConnection()
	if err != nil {
		_ = tracker.Error(err)
		return nil, err
	}

	_ = tracker.Success()
	log.Debugf("[QUIC] [conn=%s] Packet connection created successfully", tracker.ConnID())

	// 派生独立可取消 ctx:ReceiveDatagram(ctx) 会阻塞等待数据报,若无此 cancel,
	// PacketConn.Close() 无法唤醒 parked 的读 goroutine(进而拖住 proxy 有界池
	// 借出的 buffer),每路 UDP 会话结束都会泄漏一个 goroutine。
	packetCtx, packetCancel := context.WithCancel(c.ctx)
	return &PacketConn{conn: conn, tracker: tracker, ctx: packetCtx, cancel: packetCancel}, nil
}

func (c *Client) DialConn(address *tunnel.Address, tun tunnel.Tunnel) (tunnel.Conn, error) {
	conn, err := c.getOrCreateConnection()
	if err != nil {
		return nil, err
	}

	tracker := log.NewConnectionTracker("QUIC", "Stream").
		WithField("remote_addr", c.remoteAddr.String())

	log.Debugf("[QUIC] [conn=%s] Opening stream to %s", tracker.ConnID(), address.String())

	stream, err := conn.OpenStreamSync(c.ctx)
	if err != nil {
		_ = tracker.Error(err)
		log.Error(common.NewError("QUIC failed to open stream").Base(err))
		c.quicConnMutex.Lock()
		conn.CloseWithError(quic.ApplicationErrorCode(0), "stream open failed") //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		if c.quicConn == conn {
			c.quicConn = nil
		}
		c.quicConnMutex.Unlock()
		return nil, common.NewError("QUIC failed to open stream").Base(err)
	}
	_ = tracker.Success()

	log.Debugf("[QUIC] [conn=%s] Stream opened successfully", tracker.ConnID())
	return &StreamConn{Stream: stream, conn: conn, tracker: tracker}, nil
}

type StreamConn struct {
	Stream  *quic.Stream
	conn    *quic.Conn
	tracker *log.ConnectionTracker
}

func (c *StreamConn) Metadata() *tunnel.Metadata {
	return &tunnel.Metadata{}
}

func (c *StreamConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c *StreamConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

func (c *StreamConn) Read(p []byte) (int, error) {
	return c.Stream.Read(p)
}

func (c *StreamConn) Write(p []byte) (int, error) {
	return c.Stream.Write(p)
}

func (c *StreamConn) Close() error {
	if c.tracker != nil {
		c.tracker.Destroy("closed", 0, 0)
	}
	return c.Stream.Close()
}

func (c *StreamConn) SetDeadline(t time.Time) error {
	return c.Stream.SetDeadline(t)
}

func (c *StreamConn) SetReadDeadline(t time.Time) error {
	return c.Stream.SetReadDeadline(t)
}

func (c *StreamConn) SetWriteDeadline(t time.Time) error {
	return c.Stream.SetWriteDeadline(t)
}

type PacketConn struct {
	conn         *quic.Conn
	tracker      *log.ConnectionTracker
	packetBuffer chan []byte
	// ctx/cancel 用于唤醒阻塞在 ReceiveDatagram 上的读;服务端用 packetBuffer
	// 中转时可不设置(为 nil),客户端直读路径必须设置。
	ctx    context.Context
	cancel context.CancelFunc
}

func (c *PacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if err := c.conn.SendDatagram(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *PacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	if c.packetBuffer != nil {
		data, ok := <-c.packetBuffer
		if !ok {
			return 0, nil, common.NewError("QUIC packet connection closed")
		}
		n := copy(p, data)
		return n, c.conn.RemoteAddr(), nil
	}
	data, err := c.conn.ReceiveDatagram(c.receiveCtx())
	if err != nil {
		return 0, nil, err
	}
	n := copy(p, data)
	return n, c.conn.RemoteAddr(), nil
}

func (c *PacketConn) WriteWithMetadata(p []byte, m *tunnel.Metadata) (int, error) {
	if err := c.conn.SendDatagram(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *PacketConn) ReadWithMetadata(p []byte) (int, *tunnel.Metadata, error) {
	if c.packetBuffer != nil {
		data, ok := <-c.packetBuffer
		if !ok {
			return 0, nil, common.NewError("QUIC packet connection closed")
		}
		n := copy(p, data)
		return n, &tunnel.Metadata{}, nil
	}
	data, err := c.conn.ReceiveDatagram(c.receiveCtx())
	if err != nil {
		return 0, nil, err
	}
	n := copy(p, data)
	return n, &tunnel.Metadata{}, nil
}

// receiveCtx 返回用于 ReceiveDatagram 的上下文;若未设置(服务端 packetBuffer 路径)
// 回落到 context.Background(),但该路径不会走到直读分支。
func (c *PacketConn) receiveCtx() context.Context {
	if c.ctx != nil {
		return c.ctx
	}
	return context.Background()
}

func (c *PacketConn) Close() error {
	if c.cancel != nil {
		c.cancel()
	}
	if c.tracker != nil {
		c.tracker.Destroy("closed", 0, 0)
	}
	return nil
}

func (c *PacketConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c *PacketConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
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
	tlsCfg := config.FromContext(ctx, "TLS").(*tlstunnel.Config)

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
		CurvePreferences:   fingerprint.ParseCurvePreferences(tlsCfg.TLS.CurvePreferences),
	}

	quicConfig := &quic.Config{
		MaxIdleTimeout:     time.Second * time.Duration(cfg.QUIC.MaxIdleTimeout),
		MaxIncomingStreams: int64(cfg.QUIC.MaxIncomingStreams),
		// 必须开启 RFC 9221 datagram 支持,否则 ReceiveDatagram 直接返回
		// "datagram support disabled",UDP(PacketConn)路径完全不可用。
		EnableDatagrams: true,
	}

	log.Debug("QUIC client created with ALPN:", cfg.QUIC.ALPN)
	log.Debug("QUIC congestion control:", cfg.QUIC.Congestion)
	if cfg.QUIC.Congestion == "brutal" || cfg.QUIC.Congestion == "force-brutal" {
		log.Debug("QUIC brutal_up:", cfg.QUIC.BrutalUp, "bps")
		log.Debug("QUIC brutal_down:", cfg.QUIC.BrutalDown, "bps")
	}

	ctx, cancel := context.WithCancel(ctx)
	return &Client{
		underlay:       underlay,
		remoteAddr:     remoteAddr,
		sni:            cfg.RemoteHost,
		tlsConfig:      tlsConfig,
		quicConfig:     quicConfig,
		maxIdleTimeout: time.Second * time.Duration(cfg.QUIC.MaxIdleTimeout),
		congestion:     cfg.QUIC.Congestion,
		brutalUp:       cfg.QUIC.BrutalUp,
		brutalDown:     cfg.QUIC.BrutalDown,
		ctx:            ctx,
		cancel:         cancel,
	}, nil
}
