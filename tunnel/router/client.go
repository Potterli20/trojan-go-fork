package router

import (
	"context"
	"net"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	v2geodata "github.com/xtls/xray-core/common/geodata"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/common/geodata"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/Potterli20/trojan-go-fork/tunnel/freedom"
	"github.com/Potterli20/trojan-go-fork/tunnel/transport"
)

const (
	Block  = 0
	Bypass = 1
	Proxy  = 2
)

const (
	AsIs         = 0
	IPIfNonMatch = 1
	IPOnDemand   = 2
)

const MaxPacketSize = 1024 * 8

func matchDomain(list []*v2geodata.Domain, target string) bool {
	for _, d := range list {
		switch d.Type {
		case v2geodata.Domain_Full:
			domain := d.Value
			if domain == target {
				log.Tracef("domain %s hit domain(full) rule: %s", target, domain)
				return true
			}
		case v2geodata.Domain_Domain:
			domain := d.Value
			if strings.HasSuffix(target, domain) {
				idx := strings.Index(target, domain)
				if idx == 0 || target[idx-1] == '.' {
					log.Tracef("domain %s hit domain rule: %s", target, domain)
					return true
				}
			}
		case v2geodata.Domain_Substr:
			if strings.Contains(target, d.Value) {
				log.Tracef("domain %s hit keyword rule: %s", target, d.Value)
				return true
			}
		case v2geodata.Domain_Regex:
			matched, err := regexp.Match(d.Value, []byte(target))
			if err != nil {
				log.Error("invalid regex", d.Value)
				return false
			}
			if matched {
				log.Tracef("domain %s hit regex rule: %s", target, d.Value)
				return true
			}
		default:
			log.Debug("unknown rule type:", d.Type.String())
		}
	}
	return false
}

func matchIP(list []*v2geodata.CIDR, target net.IP) bool {
	isIPv6 := true
	len := net.IPv6len
	if target.To4() != nil {
		len = net.IPv4len
		isIPv6 = false
	}
	for _, c := range list {
		n := int(c.Prefix)
		mask := net.CIDRMask(n, 8*len)
		cidrIP := net.IP(c.Ip)
		if cidrIP.To4() != nil {
			if isIPv6 {
				continue
			}
		} else {
			if !isIPv6 {
				continue
			}
		}
		subnet := &net.IPNet{IP: cidrIP.Mask(mask), Mask: mask}
		if subnet.Contains(target) {
			return true
		}
	}
	return false
}

func newIPAddress(address *tunnel.Address) (*tunnel.Address, error) {
	ip, err := address.ResolveIP()
	if err != nil {
		return nil, common.NewError("router failed to resolve ip").Base(err)
	}
	newAddress := &tunnel.Address{
		IP:   ip,
		Port: address.Port,
	}
	if ip.To4() != nil {
		newAddress.AddressType = tunnel.IPv4
	} else {
		newAddress.AddressType = tunnel.IPv6
	}
	return newAddress, nil
}

type Client struct {
	domains        [3][]*v2geodata.Domain
	cidrs          [3][]*v2geodata.CIDR
	defaultPolicy  int
	domainStrategy int
	underlay       tunnel.Client
	direct         *freedom.Client
	ctx            context.Context
	cancel         context.CancelFunc
}

func (c *Client) Route(address *tunnel.Address) int {
	if address.AddressType == tunnel.DomainName {
		if c.domainStrategy == IPOnDemand {
			resolvedIP, err := newIPAddress(address)
			if err == nil {
				for i := Block; i <= Proxy; i++ {
					if matchIP(c.cidrs[i], resolvedIP.IP) {
						return i
					}
				}
			}
		}
		for i := Block; i <= Proxy; i++ {
			if matchDomain(c.domains[i], address.DomainName) {
				return i
			}
		}
		if c.domainStrategy == IPIfNonMatch {
			resolvedIP, err := newIPAddress(address)
			if err == nil {
				for i := Block; i <= Proxy; i++ {
					if matchIP(c.cidrs[i], resolvedIP.IP) {
						return i
					}
				}
			}
		}
	} else {
		for i := Block; i <= Proxy; i++ {
			if matchIP(c.cidrs[i], address.IP) {
				return i
			}
		}
	}
	return c.defaultPolicy
}

func (c *Client) DialConn(address *tunnel.Address, overlay tunnel.Tunnel) (tunnel.Conn, error) {
	policy := c.Route(address)
	switch policy {
	case Proxy:
		return c.underlay.DialConn(address, overlay)
	case Block:
		return nil, common.NewError("router blocked address: " + address.String())
	case Bypass:
		conn, err := c.direct.DialConn(address, &Tunnel{})
		if err != nil {
			return nil, common.NewError("router dial error").Base(err)
		}
		return &transport.Conn{
			Conn: conn,
		}, nil
	}
	panic("unknown policy")
}

func (c *Client) DialPacket(overlay tunnel.Tunnel) (tunnel.PacketConn, error) {
	directConn, err := net.ListenPacket("udp", "")
	if err != nil {
		return nil, common.NewError("router failed to dial udp (direct)").Base(err)
	}
	proxy, err := c.underlay.DialPacket(overlay)
	if err != nil {
		return nil, common.NewError("router failed to dial udp (proxy)").Base(err)
	}
	ctx, cancel := context.WithCancel(c.ctx)
	conn := &PacketConn{
		Client:     c,
		PacketConn: directConn,
		proxy:      proxy,
		cancel:     cancel,
		ctx:        ctx,
		packetChan: make(chan *packetInfo, 16),
	}
	go conn.packetLoop()
	return conn, nil
}

func (c *Client) Close() error {
	c.cancel()
	return c.underlay.Close()
}

type codeInfo struct {
	code     string
	strategy int
}

func loadCode(cfg *Config, prefix string) []codeInfo {
	codes := []codeInfo{}
	for _, s := range cfg.Router.Proxy {
		if strings.HasPrefix(s, prefix) {
			if left := s[len(prefix):]; len(left) > 0 {
				codes = append(codes, codeInfo{
					code:     left,
					strategy: Proxy,
				})
			} else {
				log.Warn("invalid empty rule:", s)
			}
		}
	}
	for _, s := range cfg.Router.Bypass {
		if strings.HasPrefix(s, prefix) {
			if left := s[len(prefix):]; len(left) > 0 {
				codes = append(codes, codeInfo{
					code:     left,
					strategy: Bypass,
				})
			} else {
				log.Warn("invalid empty rule:", s)
			}
		}
	}
	for _, s := range cfg.Router.Block {
		if strings.HasPrefix(s, prefix) {
			if left := s[len(prefix):]; len(left) > 0 {
				codes = append(codes, codeInfo{
					code:     left,
					strategy: Block,
				})
			} else {
				log.Warn("invalid empty rule:", s)
			}
		}
	}
	return codes
}

func NewClient(ctx context.Context, underlay tunnel.Client) (*Client, error) {
	m1 := runtime.MemStats{}
	m2 := runtime.MemStats{}
	m3 := runtime.MemStats{}
	m4 := runtime.MemStats{}

	cfg := config.FromContext(ctx, Name).(*Config)
	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(ctx)

	direct, err := freedom.NewClient(ctx, nil)
	if err != nil {
		cancel()
		return nil, common.NewError("router failed to initialize raw client").Base(err)
	}

	client := &Client{
		domains:  [3][]*v2geodata.Domain{},
		cidrs:    [3][]*v2geodata.CIDR{},
		underlay: underlay,
		direct:   direct,
		ctx:      ctx,
		cancel:   cancel,
	}
	switch strings.ToLower(cfg.Router.DomainStrategy) {
	case "as_is", "as-is", "asis":
		client.domainStrategy = AsIs
	case "ip_if_non_match", "ip-if-non-match", "ipifnonmatch":
		client.domainStrategy = IPIfNonMatch
	case "ip_on_demand", "ip-on-demand", "ipondemand":
		client.domainStrategy = IPOnDemand
	default:
		return nil, common.NewError("unknown strategy: " + cfg.Router.DomainStrategy)
	}

	switch strings.ToLower(cfg.Router.DefaultPolicy) {
	case "proxy":
		client.defaultPolicy = Proxy
	case "bypass":
		client.defaultPolicy = Bypass
	case "block":
		client.defaultPolicy = Block
	default:
		return nil, common.NewError("unknown strategy: " + cfg.Router.DomainStrategy)
	}

	runtime.ReadMemStats(&m1)

	geodataLoader := geodata.NewGeodataLoader()

	ipCode := loadCode(cfg, "geoip:")
	for _, c := range ipCode {
		code := c.code
		cidrs, err := geodataLoader.LoadIP(cfg.Router.GeoIPFilename, code)
		if err != nil {
			log.Error(err)
		} else {
			log.Infof("geoip:%s loaded", code)
			client.cidrs[c.strategy] = append(client.cidrs[c.strategy], cidrs...)
		}
	}

	runtime.ReadMemStats(&m2)

	siteCode := loadCode(cfg, "geosite:")
	for _, c := range siteCode {
		code := c.code
		attrWanted := ""
		if attrIdx := strings.Index(code, "@"); attrIdx > 0 {
			if !strings.HasSuffix(code, "@") {
				code = c.code[:attrIdx]
				attrWanted = c.code[attrIdx+1:]
			} else {
				log.Warnf("geosite:%s invalid", code)
				continue
			}
		} else if attrIdx == 0 {
			log.Warnf("geosite:%s invalid", code)
			continue
		}

		domainList, err := geodataLoader.LoadSite(cfg.Router.GeoSiteFilename, code)
		if err != nil {
			log.Error(err)
		} else {
			found := false
			if attrWanted != "" {
				for _, domain := range domainList {
					for _, attr := range domain.Attribute {
						if strings.EqualFold(attrWanted, attr.Key) {
							client.domains[c.strategy] = append(client.domains[c.strategy], domain)
							found = true
						}
					}
				}
			} else {
				client.domains[c.strategy] = append(client.domains[c.strategy], domainList...)
				found = true
			}
			if found {
				log.Infof("geosite:%s loaded", c.code)
			} else {
				log.Errorf("geosite:%s not found", c.code)
			}
		}
	}

	runtime.ReadMemStats(&m3)

	domainInfo := loadCode(cfg, "domain:")
	for _, info := range domainInfo {
		client.domains[info.strategy] = append(client.domains[info.strategy], &v2geodata.Domain{
			Type:      v2geodata.Domain_Domain,
			Value:     strings.ToLower(info.code),
			Attribute: nil,
		})
	}

	keywordInfo := loadCode(cfg, "keyword:")
	for _, info := range keywordInfo {
		client.domains[info.strategy] = append(client.domains[info.strategy], &v2geodata.Domain{
			Type:      v2geodata.Domain_Substr,
			Value:     strings.ToLower(info.code),
			Attribute: nil,
		})
	}

	regexInfo := loadCode(cfg, "regex:")
	for _, info := range regexInfo {
		if _, err := regexp.Compile(info.code); err != nil {
			return nil, common.NewError("invalid regular expression: " + info.code).Base(err)
		}
		client.domains[info.strategy] = append(client.domains[info.strategy], &v2geodata.Domain{
			Type:      v2geodata.Domain_Regex,
			Value:     info.code,
			Attribute: nil,
		})
	}

	regexpInfo := loadCode(cfg, "regexp:")
	for _, info := range regexpInfo {
		if _, err := regexp.Compile(info.code); err != nil {
			return nil, common.NewError("invalid regular expression: " + info.code).Base(err)
		}
		client.domains[info.strategy] = append(client.domains[info.strategy], &v2geodata.Domain{
			Type:      v2geodata.Domain_Regex,
			Value:     info.code,
			Attribute: nil,
		})
	}

	fullInfo := loadCode(cfg, "full:")
	for _, info := range fullInfo {
		client.domains[info.strategy] = append(client.domains[info.strategy], &v2geodata.Domain{
			Type:      v2geodata.Domain_Full,
			Value:     strings.ToLower(info.code),
			Attribute: nil,
		})
	}

	cidrInfo := loadCode(cfg, "cidr:")
	for _, info := range cidrInfo {
		tmp := strings.Split(info.code, "/")
		if len(tmp) != 2 {
			return nil, common.NewError("invalid cidr: " + info.code)
		}
		ip := net.ParseIP(tmp[0])
		if ip == nil {
			return nil, common.NewError("invalid cidr ip: " + info.code)
		}
		prefix, err := strconv.ParseInt(tmp[1], 10, 32)
		if err != nil {
			return nil, common.NewError("invalid prefix").Base(err)
		}
		client.cidrs[info.strategy] = append(client.cidrs[info.strategy], &v2geodata.CIDR{
			Ip:     ip,
			Prefix: uint32(prefix),
		})
	}

	log.Info("router client created")

	runtime.ReadMemStats(&m4)

	log.Debugf("GeoIP rules -> Alloc: %s; TotalAlloc: %s", common.HumanFriendlyTraffic(m2.Alloc-m1.Alloc), common.HumanFriendlyTraffic(m2.TotalAlloc-m1.TotalAlloc))
	log.Debugf("GeoSite rules -> Alloc: %s; TotalAlloc: %s", common.HumanFriendlyTraffic(m3.Alloc-m2.Alloc), common.HumanFriendlyTraffic(m3.TotalAlloc-m2.TotalAlloc))
	log.Debugf("Manual rules -> Alloc: %s; TotalAlloc: %s", common.HumanFriendlyTraffic(m4.Alloc-m3.Alloc), common.HumanFriendlyTraffic(m4.TotalAlloc-m3.TotalAlloc))

	return client, nil
}