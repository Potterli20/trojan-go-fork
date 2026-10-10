package trojan

import (
	"bytes"
	"context"
	"errors"
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
	sent atomic.Uint64
	recv atomic.Uint64

	metadata *tunnel.Metadata
	user     statistic.User
	// headerMu 串行化协议头写入；headerWritten 标记头已写出；
	// headerFailed 表示头已写出失败——流上可能残留部分字节，连接不可再复用
	headerMu      sync.Mutex
	headerWritten atomic.Bool
	headerFailed  bool
	ctx           context.Context
	cancel        context.CancelFunc
	net.Conn
}

func (c *OutboundConn) Metadata() *tunnel.Metadata {
	return c.metadata
}

func (c *OutboundConn) WriteHeader(payload []byte) (bool, error) {
	if c.headerWritten.Load() {
		return false, nil
	}
	c.headerMu.Lock()
	defer c.headerMu.Unlock()
	if c.headerWritten.Load() {
		return false, nil
	}
	if c.headerFailed {
		return false, common.NewError("trojan header write previously failed, connection is unusable")
	}
	hash := c.user.GetHash()
	buf := bytes.NewBuffer(make([]byte, 0, MaxPacketSize))
	crlf := []byte{0x0d, 0x0a}
	buf.Write([]byte(hash))
	buf.Write(crlf)
	c.metadata.WriteTo(buf) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	buf.Write(crlf)
	if payload != nil {
		buf.Write(payload)
	}
	if _, err := c.Conn.Write(buf.Bytes()); err != nil {
		c.headerFailed = true
		log.Error("[Trojan] Failed to write header:", err)
		return false, err
	}
	c.headerWritten.Store(true)
	if log.ShouldLog(log.DebugLevel) {
		log.Debug("[Trojan] Header written for", c.metadata.Address)
		log.Debug("[Trojan] Target:", c.metadata.Command, c.metadata.Address)
	}
	return true, nil
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
	if n >= 0 {
		c.sent.Add(uint64(n)) //gosec:disable -- n >= 0 已保证安全
	}
	return n, err
}

func (c *OutboundConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err != nil && err != io.EOF && log.ShouldLog(log.DebugLevel) {
		log.Debug("[Trojan] Connection read error:", err)
	}
	c.user.AddRecvTraffic(n)
	if n >= 0 {
		c.recv.Add(uint64(n)) //gosec:disable -- n >= 0 已保证安全
	}
	return n, err
}

func (c *OutboundConn) Close() error {
	c.cancel()
	if log.ShouldLog(log.InfoLevel) {
		log.Info("[Trojan] Connection to", c.metadata, "closed", "sent:", common.HumanFriendlyTraffic(c.sent.Load()), "recv:", common.HumanFriendlyTraffic(c.recv.Load()))
	}
	if err := c.Conn.Close(); err != nil {
		// 关停时 proxy 的转发环和本 tunnel 都会关同一条流，"已经关过"不是故障。
		// 把它记成 ERROR 并上抛，会让一次正常关停看起来像错误（也污染退出码判断）。
		if errors.Is(err, net.ErrClosed) {
			return nil
		}
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
	wg       sync.WaitGroup
}

func (c *Client) Close() error {
	log.Info("[Trojan] Closing client")
	c.cancel()
	c.wg.Wait()
	// NewClient 用 statistic.NewAuthenticator(c.ctx, …) 取认证器，它按 ctx 为键登记在
	// 全局 createdAuth 里（只增不删）。服务端早就成对调用了 ReleaseAuthenticator，
	// 客户端此前漏了：每建一个 client 实例就永久留住一条 map 项、整棵 User 对象图
	// （含 ipTable）以及 sqlite 的 DB 句柄——Authenticator.Close 是唯一的
	// pst.Close() 入口，客户端路径永远走不到。wg.Wait 之后再释放，避免还在上报的
	// 批处理 goroutine 撞上已关闭的持久化层。
	if err := statistic.ReleaseAuthenticator(c.ctx); err != nil {
		log.Error("[Trojan] Failed to release authenticator:", err)
	}
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
		_ = tracker.Error(err)
		return nil, common.NewError("failed to dial underlying connection").Base(err)
	}
	_ = tracker.Success()

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

	c.wg.Go(func() {
		select {
		case <-time.After(time.Millisecond * 100):
			newConn.WriteHeader(nil) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		case <-newConn.ctx.Done():
			return
		}
	})

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
		_ = tracker.Error(err)
		return nil, common.NewError("failed to dial underlying connection for UDP").Base(err)
	}
	_ = tracker.Success()

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

	// 从这里起任何一条错误返回都必须把认证器归还：statistic.NewAuthenticator 已把它按
	// ctx 登记进全局 createdAuth，而 memory 后端内部另派了自己的 ctx（注释原话：
	// “生命周期由自身 Close 控制，不依赖调用方取消父 ctx”），所以本函数后面那句
	// cancel() 停不了它——配了 sqlite 时 batchTrafficUpdater 与 DB 句柄会永远跑下去。
	// 已知触发点：口令列表为空导致的 "no valid user found"。
	authReleased := false
	defer func() {
		if !authReleased {
			if err := statistic.ReleaseAuthenticator(ctx); err != nil {
				log.Warn("[Trojan] Failed to release authenticator:", err)
			}
		}
	}()

	cfg := config.FromContext(ctx, Name).(*Config)
	if log.ShouldLog(log.DebugLevel) {
		log.Debug("[Trojan] RemoteHost:", cfg.RemoteHost)
		log.Debug("[Trojan] RemotePort:", cfg.RemotePort)
		log.Debug("[Trojan] API Enabled:", cfg.API.Enabled)
	}

	if cfg.API.Enabled {
		log.Info("[Trojan] Starting API service")
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

	c := &Client{
		underlay: client,
		ctx:      ctx,
		user:     user,
		cancel:   cancel,
	}

	if cfg.API.Enabled {
		c.wg.Go(func() {
			api.RunService(ctx, Name+"_CLIENT", auth)
		})
	}

	authReleased = true // 交棒给 Client.Close()
	return c, nil
}
