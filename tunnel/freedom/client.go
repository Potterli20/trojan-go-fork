package freedom

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Potterli20/socks5-fork"
	"golang.org/x/net/proxy"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type Client struct {
	preferIPv4      bool
	noDelay         bool
	keepAlive       bool
	fastOpen        bool
	ctx             context.Context
	cancel          context.CancelFunc
	forwardProxy    bool
	proxyAddr       *tunnel.Address
	username        string
	password        string
	outboundLocalIP net.IP
	outboundFwmark  int
}

// forwardHandshakeTimeout 覆盖 forward-proxy 这一跳的整个过程：连上配置里那个
// SOCKS 代理、走完 SOCKS 认证、再把 CONNECT 的应答读回来。直连路径走的是
// common.Dial(c.ctx, 30s)，而这一条原先交给 x/net/proxy 的默认 dialer——既没有
// 超时，也完全不看 c.ctx。代理收下 TCP 却不回握手字节（半死的上游链路正是这个
// 形态）时 DialConn 永不返回，而它的调用方是 relay 的会话协程：一次撞死就永久占用
// 一条协程加一个半开 socket，Proxy.Close 的 5s waitBounded 也只是放弃等待，叫不醒它。
//
// 做成 var 是给判据留的注入口：forward_dial_test.go 把它缩到几百毫秒，用来区分
// "握手截止到底解除没有"。理由同 tunnel/tproxy 的 `var dialUDP = DialUDP`，
// 别当冗余代码删掉。
var forwardHandshakeTimeout = 30 * time.Second

// socksAssociateTimeout 覆盖 UDP ASSOCIATE 这一跳的整个握手：连上代理、方法与用户名
// 口令协商、再把 ASSOCIATE 的应答读回来。
//
// 库（Potterli20/socks5-fork）只在 TCPTimeout != 0 时才 SetDeadline，而 DialPacket 过去
// 传的是 0。代理受理 TCP 却不回握手字节（半死的上游链路正是这个形态）时 Negotiate/Request
// 里的读永不返回，而调用方是 relay 的会话协程：一次撞死就永久占用一条协程加一个半开 socket，
// Client.Close 的 cancel 叫不醒它——AGENTS.md 里 forwardDialer 那条修复只覆盖了 DialConn
// 这个姊妹分支，DialPacket 漏了。
//
// 做成 var 是给判据留注入口（同 forwardHandshakeTimeout）。时限粒度是**秒**，因为库的
// TCPTimeout 字段就是秒；换算见 socksTimeoutSeconds。
var socksAssociateTimeout = 30 * time.Second

// socksTimeoutSeconds 把 socksAssociateTimeout 换算成库要的整秒。向上取整：向下取整会把
// 300ms 变成 0，而 0 在库里恰好是"不设截止"——判据缩小的时限会静默退回被测的那个缺陷。
func socksTimeoutSeconds() int {
	sec := int(socksAssociateTimeout / time.Second)
	if socksAssociateTimeout%time.Second != 0 {
		sec++
	}
	if sec < 1 {
		sec = 1
	}
	return sec
}

// forwardDialer 给 x/net/proxy 补上它自己没有的两件事：把"连代理"这一跳接回
// c.ctx（域名代理地址的 DNS 解析也一并接回去），以及给紧随其后的 SOCKS 握手挂一个
// 截止时间。x/net 的 socks 实现对这个 conn 是裸 read/write，所以整体截止就是它的
// 终点；context.AfterFunc 再让 Client.Close 能当场打断阻塞中的握手。
type forwardDialer struct {
	ctx context.Context

	mu       sync.Mutex
	tunneled *handshakeConn
}

// Dial 是 proxy.Dialer 的实现：x/net 用它去连配置的代理，拿到的 conn 随后就是
// SOCKS 握手与会话隧道的载体。
func (d *forwardDialer) Dial(network, address string) (net.Conn, error) {
	conn, err := common.Dial(d.ctx, common.DialConfig{
		Network: network,
		Address: address,
		Timeout: forwardHandshakeTimeout,
	})
	if err != nil {
		return nil, err
	}
	hs := &handshakeConn{Conn: conn}
	if err := hs.arm(d.ctx, forwardHandshakeTimeout); err != nil {
		conn.Close() //gosec:disable -- 错误忽略：握手尚未建立
		return nil, err
	}
	d.mu.Lock()
	d.tunneled = hs
	d.mu.Unlock()
	return hs, nil
}

// finish 撤掉握手时限与 ctx 监视。DialConn 一返回就必须做（含错误路径）：这条 conn
// 马上要被当作会话隧道交给中继长期持有，残留的截止会在几十秒后凭空掐断一条好端端
// 的会话；而 AfterFunc 不停掉的话，将来 Client.Close 会去掐一条与本次拨号无关的活连接。
func (d *forwardDialer) finish() {
	d.mu.Lock()
	hs := d.tunneled
	d.tunneled = nil
	d.mu.Unlock()
	if hs != nil {
		hs.disarm()
	}
}

type handshakeConn struct {
	net.Conn
	stopWatch func() bool
	disarmed  atomic.Bool
}

func (h *handshakeConn) arm(ctx context.Context, timeout time.Duration) error {
	if err := h.Conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	h.stopWatch = context.AfterFunc(ctx, func() {
		// 取消即打断：让阻塞在握手读上的这次调用当场带错误返回
		h.Conn.SetDeadline(time.UnixMilli(1)) //gosec:disable -- 错误忽略：取消路径打断阻塞中的握手
	})
	return nil
}

func (h *handshakeConn) disarm() {
	if h.disarmed.CompareAndSwap(false, true) {
		h.stopWatch()
		h.Conn.SetDeadline(time.Time{}) //gosec:disable -- 错误忽略：交出隧道前解除握手时限
	}
}

func (c *Client) DialConn(addr *tunnel.Address, _ tunnel.Tunnel) (tunnel.Conn, error) {
	// forward proxy
	if c.forwardProxy {
		var auth *proxy.Auth
		if c.username != "" {
			auth = &proxy.Auth{
				User:     c.username,
				Password: c.password,
			}
		}
		fd := &forwardDialer{ctx: c.ctx}
		dialer, err := proxy.SOCKS5("tcp", c.proxyAddr.String(), auth, fd)
		if err != nil {
			return nil, common.NewError("freedom failed to init socks dialer")
		}
		defer fd.finish()
		conn, err := dialer.Dial("tcp", addr.String())
		if err != nil {
			return nil, common.NewError("freedom failed to dial target address via socks proxy " + addr.String()).Base(err)
		}
		return &Conn{
			Conn: conn,
		}, nil
	}
	return c.DialConnDirect(addr, nil)
}

// DialConnDirect 直接拨号到目标地址，绕过 forward_proxy 配置。
// 用于 router 的 bypass 分支：即使配置了 forward_proxy，bypass 规则命中时也应直连目标。
func (c *Client) DialConnDirect(addr *tunnel.Address, _ tunnel.Tunnel) (tunnel.Conn, error) {
	localIP, fwmark := getGlobalOutbound()
	var localAddr net.Addr
	if localIP != nil {
		localAddr = &net.TCPAddr{IP: localIP}
	}
	log.Tracef("freedom dial-direct start: target=%s forward_proxy_enabled=%v local_ip=%v fwmark=%d tfo=%v",
		addr.String(), c.forwardProxy, localIP, fwmark, c.fastOpen)
	start := time.Now()
	dialCfg := common.DialConfig{
		Network:       "tcp",
		Address:       addr.String(),
		EnableTFO:     c.fastOpen,
		Timeout:       30 * time.Second,
		KeepAlive:     c.keepAlive,
		NoDelay:       c.noDelay,
		PreferIPv4:    c.preferIPv4,
		RetryCount:    1,
		RetryInterval: 500 * time.Millisecond,
		LocalAddr:     localAddr,
		Control:       fwmarkControl(fwmark),
	}
	tcpConn, err := common.Dial(c.ctx, dialCfg)
	if err != nil {
		log.Debugf("freedom dial-direct failed: target=%s elapsed=%s err=%v",
			addr.String(), time.Since(start), err)
		return nil, common.NewError("freedom failed to dial " + addr.String()).Base(err)
	}
	log.Tracef("freedom dial-direct ok: target=%s elapsed=%s local=%s remote=%s",
		addr.String(), time.Since(start), tcpConn.LocalAddr(), tcpConn.RemoteAddr())
	return &Conn{
		Conn: tcpConn,
	}, nil
}

func (c *Client) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	if c.forwardProxy {
		// udpTimeout 留 0：库里的 UDPTimeout 只作用于 Client.Read/Write/SetDeadline，
		// 而本层的 UDP 负载走的是 SocksPacketConn 自己那块 PacketConn，不经过这个 Client，
		// 给它加截止只会让 Close 路径上的行为更难推断。
		socksClient, err := socks5.NewClient(c.proxyAddr.String(), c.username, c.password, socksTimeoutSeconds(), 0)
		if err != nil {
			return nil, common.NewError("freedom failed to create socks client").Base(err)
		}
		// socks5.Client.Close 在 TCPConn 为 nil 时会 panic，按建立阶段分别清理
		closeSocksTCP := func() {
			if socksClient.TCPConn != nil {
				_ = socksClient.TCPConn.Close()
			}
		}
		if err := socksClient.Negotiate(&net.TCPAddr{}); err != nil {
			closeSocksTCP()
			return nil, common.NewError("freedom failed to negotiate socks").Base(err)
		}
		a, addr, port, err := socks5.ParseAddress("1.1.1.1:53")
		if err != nil {
			closeSocksTCP()
			return nil, common.NewError("freedom failed to parse address").Base(err)
		}
		resp, err := socksClient.Request(socks5.NewRequest(socks5.CmdUDP, a, addr, port))
		if err != nil {
			closeSocksTCP()
			return nil, common.NewError("freedom failed to dial udp to socks").Base(err)
		}
		// 0.0.0.0:0 让内核自动选择绑定地址和端口，是 forward-proxy 模式下
		// UDP relay 的标准做法（监听所有接口、随机端口），有意为之。
		packetConn, err := net.ListenPacket("udp", "0.0.0.0:0")
		if err != nil {
			closeSocksTCP()
			return nil, common.NewError("freedom failed to listen udp").Base(err)
		}
		socksAddr, err := net.ResolveUDPAddr("udp", resp.Address())
		if err != nil {
			packetConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			closeSocksTCP()
			return nil, common.NewError("freedom recv invalid socks bind addr").Base(err)
		}
		if socksAddr.IP.Equal(net.IPv4zero) {
			ip, err := c.proxyAddr.ResolveIP()
			if err != nil {
				packetConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
				closeSocksTCP()
				return nil, common.NewError("freedom failed to resolve ip").Base(err)
			}
			socksAddr.IP = ip
		}
		return &SocksPacketConn{
			PacketConn:  packetConn,
			socksAddr:   socksAddr,
			socksClient: socksClient,
		}, nil
	}
	network := "udp"
	if c.preferIPv4 {
		network = "udp4"
	}
	localIP, fwmark := getGlobalOutbound()
	listenAddr := ""
	if localIP != nil {
		listenAddr = (&net.UDPAddr{IP: localIP, Port: 0}).String()
	}
	lc := net.ListenConfig{}
	if ctrl := fwmarkControl(fwmark); ctrl != nil {
		lc.Control = ctrl
	}
	udpConn, err := lc.ListenPacket(c.ctx, network, listenAddr)
	if err != nil {
		return nil, common.NewError("freedom failed to listen udp socket").Base(err)
	}
	return &PacketConn{
		UDPConn: udpConn.(*net.UDPConn),
	}, nil
}

func (c *Client) Close() error {
	c.cancel()
	return nil
}

func NewClient(ctx context.Context, _ tunnel.Client) (*Client, error) {
	cfg := config.FromContext(ctx, Name).(*Config)
	addr := tunnel.NewAddressFromHostPort("tcp", cfg.ForwardProxy.ProxyHost, cfg.ForwardProxy.ProxyPort)

	var outboundLocalIP net.IP
	if cfg.OutboundLocalAddr != "" {
		outboundLocalIP = net.ParseIP(cfg.OutboundLocalAddr)
		if outboundLocalIP == nil {
			return nil, common.NewError("freedom: invalid outbound_local_addr: " + cfg.OutboundLocalAddr)
		}
	}

	fwmark := cfg.OutboundFwmark
	if fwmark != 0 && !fwmarkSupported {
		log.Warn("freedom: outbound_fwmark is set but not supported on this platform; ignored")
		fwmark = 0
	}

	ctx, cancel := context.WithCancel(ctx)
	SetGlobalOutbound(outboundLocalIP, fwmark)
	return &Client{
		ctx:             ctx,
		cancel:          cancel,
		noDelay:         cfg.TCP.NoDelay,
		keepAlive:       cfg.TCP.KeepAlive,
		preferIPv4:      cfg.TCP.PreferIPV4,
		fastOpen:        cfg.TCP.FastOpen,
		forwardProxy:    cfg.ForwardProxy.Enabled,
		proxyAddr:       addr,
		username:        cfg.ForwardProxy.Username,
		password:        cfg.ForwardProxy.Password,
		outboundLocalIP: outboundLocalIP,
		outboundFwmark:  fwmark,
	}, nil
}
