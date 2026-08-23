package freedom

import (
	"context"
	"net"
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
		dialer, err := proxy.SOCKS5("tcp", c.proxyAddr.String(), auth, proxy.Direct)
		if err != nil {
			return nil, common.NewError("freedom failed to init socks dialer")
		}
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
		socksClient, err := socks5.NewClient(c.proxyAddr.String(), c.username, c.password, 0, 0)
		if err != nil {
			return nil, common.NewError("freedom failed to create socks client").Base(err)
		}
		if err := socksClient.Negotiate(&net.TCPAddr{}); err != nil {
			return nil, common.NewError("freedom failed to negotiate socks").Base(err)
		}
		a, addr, port, err := socks5.ParseAddress("1.1.1.1:53")
		if err != nil {
			return nil, common.NewError("freedom failed to parse address").Base(err)
		}
		resp, err := socksClient.Request(socks5.NewRequest(socks5.CmdUDP, a, addr, port))
		if err != nil {
			return nil, common.NewError("freedom failed to dial udp to socks").Base(err)
		}
		// TODO fix hardcoded localhost
		packetConn, err := net.ListenPacket("udp", "0.0.0.0:0")
		if err != nil {
			return nil, common.NewError("freedom failed to listen udp").Base(err)
		}
		socksAddr, err := net.ResolveUDPAddr("udp", resp.Address())
		if err != nil {
			return nil, common.NewError("freedom recv invalid socks bind addr").Base(err)
		}
		if socksAddr.IP.Equal(net.IPv4zero) {
			ip, err := c.proxyAddr.ResolveIP()
			if err != nil {
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
