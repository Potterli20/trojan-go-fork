package freedom

import (
	"context"
	"net"

	"github.com/txthinking/socks5"
	"golang.org/x/net/proxy"

	"github.com/p4gefau1t/trojan-go/common"
	"github.com/p4gefau1t/trojan-go/config"
	"github.com/p4gefau1t/trojan-go/log"
	"github.com/p4gefau1t/trojan-go/tunnel"
)

type Client struct {
	preferIPv4      bool
	noDelay         bool
	keepAlive       bool
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
	network := "tcp"
	if c.preferIPv4 {
		network = "tcp4"
	}
	localIP, fwmark := getGlobalOutbound()
	dialer := new(net.Dialer)
	if localIP != nil {
		dialer.LocalAddr = &net.TCPAddr{IP: localIP}
	}
	if ctrl := fwmarkControl(fwmark); ctrl != nil {
		dialer.Control = ctrl
	}
	tcpConn, err := dialer.DialContext(c.ctx, network, addr.String())
	if err != nil {
		return nil, common.NewError("freedom failed to dial " + addr.String()).Base(err)
	}

	tcpConn.(*net.TCPConn).SetKeepAlive(c.keepAlive)
	tcpConn.(*net.TCPConn).SetNoDelay(c.noDelay)
	return &Conn{
		Conn: tcpConn,
	}, nil
}

func (c *Client) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	if c.forwardProxy {
		socksClient, err := socks5.NewClient(c.proxyAddr.String(), c.username, c.password, 0, 0)
		common.Must(err)
		if err := socksClient.Negotiate(&net.TCPAddr{}); err != nil {
			return nil, common.NewError("freedom failed to negotiate socks").Base(err)
		}
		a, addr, port, err := socks5.ParseAddress("1.1.1.1:53") // useless address
		common.Must(err)
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
		forwardProxy:    cfg.ForwardProxy.Enabled,
		proxyAddr:       addr,
		username:        cfg.ForwardProxy.Username,
		password:        cfg.ForwardProxy.Password,
		outboundLocalIP: outboundLocalIP,
		outboundFwmark:  fwmark,
	}, nil
}
