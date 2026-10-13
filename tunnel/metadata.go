package tunnel

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/log"
)

type Command byte

type Metadata struct {
	Command
	*Address
}

func (r *Metadata) ReadFrom(rr io.Reader) (int64, error) {
	var byteBuf [1]byte
	n, err := io.ReadFull(rr, byteBuf[:])
	if err != nil {
		return int64(n), err
	}
	r.Command = Command(byteBuf[0])
	r.Address = new(Address)
	n2, err := r.Address.ReadFrom(rr)
	if err != nil {
		return int64(n) + n2, common.NewError("failed to unmarshal address: ").Base(err)
	}
	return int64(n) + n2, nil
}

func (r *Metadata) WriteTo(w io.Writer) (int64, error) {
	if r.Address == nil {
		return 0, common.NewError("cannot serialize metadata with nil address")
	}
	buf, err := r.AppendTo(make([]byte, 0, 1+r.Address.wireLength()))
	if err != nil {
		return 0, err
	}
	// use tcp by default
	r.Address.NetworkType = "tcp"
	n, err := w.Write(buf)
	return int64(n), err
}

// AppendTo 把 Command + 地址按线上格式追加到 buf 末尾，返回增长后的切片。
// 与 WriteTo 唯一的区别是不落地到 io.Writer，让手里已经有缓冲区的调用方零分配。
func (r *Metadata) AppendTo(buf []byte) ([]byte, error) {
	if r.Address == nil {
		return buf, common.NewError("cannot serialize metadata with nil address")
	}
	buf = append(buf, byte(r.Command))
	return r.Address.AppendTo(buf)
}

func (r *Metadata) Network() string {
	if r.Address == nil {
		return ""
	}
	return r.Address.Network()
}

func (r *Metadata) String() string {
	if r.Address == nil {
		return ""
	}
	return r.Address.String()
}

type AddressType byte

const (
	IPv4       AddressType = 1
	DomainName AddressType = 3
	IPv6       AddressType = 4
)

type Address struct {
	DomainName  string
	Port        int
	NetworkType string
	net.IP
	AddressType

	// resolveMu 保护 resolvedIP。为什么需要锁：一个 *Address 会被多条并发会话**共享**
	// ——dokodemo 的 s.targetAddr 每实例一个，却塞进每条 TCP 会话的 targetMetadata 和
	// 每个 UDP 会话的 fixedMetadata（server.go:34/:135）；freedom 的 c.proxyAddr 同理。
	// relay 给每条会话各起一个 goroutine，所以这里是真实的并发读写，不是理论风险。
	//
	// 旧实现把 DNS 结果写回导出的 net.IP 字段。那是无效的：域名地址的序列化与
	// String() 都只看 DomainName，没人读这个 IP；而 ReadFrom / AppendTo 的 IPv4/IPv6
	// 分支会不加锁地读 IP。也就是说这次写回一个读者用、写者不加锁的字段，纯粹是给
	// -race 送分。现在缓存挪到非导出字段并在锁内读写，net.IP 只在构造/解析时定型。
	// 保留缓存本身是必要的：router 对同一条连接会解析两次（client.go 判定策略、
	// conn.go 走 UDP 路径），去掉缓存就是每次拨号多一轮 DNS。
	resolveMu  sync.Mutex
	resolvedIP net.IP
}

func (a *Address) String() string {
	switch a.AddressType {
	case IPv4:
		return fmt.Sprintf("%s:%d", a.IP.String(), a.Port)
	case IPv6:
		return fmt.Sprintf("[%s]:%d", a.IP.String(), a.Port)
	case DomainName:
		return fmt.Sprintf("%s:%d", a.DomainName, a.Port)
	default:
		return "INVALID_ADDRESS_TYPE"
	}
}

func (a *Address) Network() string {
	return a.NetworkType
}

func (a *Address) ResolveIP() (net.IP, error) {
	//LOG
	log.Debug("ResolveIP: network type :" + a.NetworkType + " Domain: " + a.DomainName)
	if a.AddressType == IPv4 || a.AddressType == IPv6 {
		// 这条分支读的是构造/解析时定型的 IP，之后没有任何写入点，所以无锁可读
		return a.IP, nil
	}
	log.Debug("ResolveIP Start! domain: ", a.DomainName, " PORT: ", a.Port)

	a.resolveMu.Lock()
	cached := a.resolvedIP
	a.resolveMu.Unlock()
	if cached != nil {
		return cached, nil
	}

	ipAddr, err := net.ResolveIPAddr("ip", a.DomainName)
	if err != nil {
		return nil, err
	}
	log.Debug("ResolveIP Done! IP: ", ipAddr.IP)

	// 并发首次解析可能做两次 DNS，这跟改造前一样，不是回退；要紧的是缓存只落一次、
	// 且绝不写导出的 net.IP（那会和 AppendTo/String 的无锁读构成竞争）
	a.resolveMu.Lock()
	if a.resolvedIP == nil {
		a.resolvedIP = ipAddr.IP
	}
	resolved := a.resolvedIP
	a.resolveMu.Unlock()
	return resolved, nil
}

func NewAddressFromAddr(network, addr string) (*Address, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, common.NewError("failed to split host port").Base(err)
	}
	port, err := strconv.ParseInt(portStr, 10, 32)
	if err != nil {
		return nil, common.NewError("failed to parse port number").Base(err)
	}
	if port < 0 || port > 65535 {
		return nil, common.NewError("invalid port number")
	}
	address := NewAddressFromHostPort(network, host, int(port))
	if address == nil {
		return nil, common.NewError("unsupported network type: " + network)
	}
	return address, nil
}

func NewAddressFromHostPort(network string, host string, port int) *Address {
	if network != "tcp" && network != "udp" {
		log.Error("unsupported network type: " + network + " HOST: " + host)
		return nil
	}

	log.Debug("network type : " + network + " HOST: " + host)

	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() != nil {
			return &Address{
				IP:          ip,
				Port:        port,
				AddressType: IPv4,
				NetworkType: network,
			}
		}
		return &Address{
			IP:          ip,
			Port:        port,
			AddressType: IPv6,
			NetworkType: network,
		}
	}
	return &Address{
		DomainName:  host,
		Port:        port,
		AddressType: DomainName,
		NetworkType: network,
	}
}

// NewAddressFromNetAddr 从 net.Addr 组装 Address；底层 UDP/TCP 地址直接取字段，
// 不再走一遍 "String() → SplitHostPort → ParseInt → ParseIP" 的往返。
//
// 这条路径在 UDP 中继上是**每包**执行一次（tunnel/freedom 与 tunnel/router 的
// 读方向），旧写法的开销主要是每次解析里那句 log.Debug：门禁生效前实测
// 11759 ns/op、958 B/op、28 allocs/op，而这几个字段本来就在手上。
//
// 行为差异只有一处，且是往好的方向：带 zone 的链路本地 IPv6（fe80::1%eth0）
// 旧路径会因为 net.ParseIP 不认 zone 而把它当成**域名**存进 Address，
// 后续按域名匹配的路由规则和 WriteTo 的 ATYP 都跟着错；现在按 IPv6 处理。
// 其余情况（地址类型不合预期、空 IP、network 不是 tcp/udp）一律退回旧的解析
// 路径，保持行为一致。
func NewAddressFromNetAddr(network string, addr net.Addr) (*Address, error) {
	var ip net.IP
	var port int
	switch a := addr.(type) {
	case *net.UDPAddr:
		ip, port = a.IP, a.Port
	case *net.TCPAddr:
		ip, port = a.IP, a.Port
	default:
		return NewAddressFromAddr(network, addr.String())
	}
	if len(ip) == 0 || (network != "tcp" && network != "udp") {
		return NewAddressFromAddr(network, addr.String())
	}
	address := &Address{
		IP:          ip,
		Port:        port,
		NetworkType: network,
	}
	if ip.To4() != nil {
		address.AddressType = IPv4
	} else {
		address.AddressType = IPv6
	}
	return address, nil
}

func (a *Address) ReadFrom(r io.Reader) (int64, error) {
	var byteBuf [1]byte
	n, err := io.ReadFull(r, byteBuf[:])
	if err != nil {
		return int64(n), common.NewError("unable to read ATYP").Base(err)
	}
	a.AddressType = AddressType(byteBuf[0])
	total := int64(n)
	switch a.AddressType {
	case IPv4:
		var buf [6]byte
		n, err := io.ReadFull(r, buf[:])
		total += int64(n)
		if err != nil {
			return total, common.NewError("failed to read IPv4").Base(err)
		}
		a.IP = buf[0:4]
		a.Port = common.SafeIntFromUint16(binary.BigEndian.Uint16(buf[4:6]))
	case IPv6:
		var buf [18]byte
		n, err := io.ReadFull(r, buf[:])
		total += int64(n)
		if err != nil {
			return total, common.NewError("failed to read IPv6").Base(err)
		}
		a.IP = buf[0:16]
		a.Port = common.SafeIntFromUint16(binary.BigEndian.Uint16(buf[16:18]))
	case DomainName:
		n, err := io.ReadFull(r, byteBuf[:])
		total += int64(n)
		// 长度必须立刻升到 int：byte 参与加法会在 254/255 处回绕
		// （254→0、255→1），make 出来的缓冲比域名短，随后的 buf[:length] 与
		// buf[length:length+2] 直接切片越界 panic —— 一个 254/255 字节的主机名
		// 既读不回来，又能把进程打崩（socks / freedom / simplesocks 这几条调用链
		// 上没有 recover，只有 mux / tls / trojan 三处有）。
		length := int(byteBuf[0])
		if err != nil {
			return total, common.NewError("failed to read domain name length")
		}
		buf := make([]byte, length+2)
		n, err = io.ReadFull(r, buf)
		total += int64(n)
		if err != nil {
			return total, common.NewError("failed to read domain name")
		}
		// check if the domain name is actually an IP address
		if ip := net.ParseIP(string(buf[:length])); ip != nil {
			a.IP = ip
			if ip.To4() != nil {
				a.AddressType = IPv4
			} else {
				a.AddressType = IPv6
			}
		} else {
			a.DomainName = string(buf[:length])
		}
		a.Port = common.SafeIntFromUint16(binary.BigEndian.Uint16(buf[length : length+2]))
	default:
		return total, common.NewError("invalid address type " + strconv.FormatInt(int64(a.AddressType), 10))
	}
	return total, nil
}

// AppendTo 把地址按线上格式追加到 buf 末尾，返回增长后的切片。
//
// 这里从此是地址格式**唯一定义处**：WriteTo 变成"AppendTo + 一次 Write"的薄封装，
// 两者不可能漂移。之所以要做这个形态，是因为 WriteTo 只能逐段 w.Write，而每个
// []byte{...} 复合字面量都会逃逸到堆上——实测域名地址 4 次分配（含一次域名整串的
// 转换）、IP 地址 2 次（本机 Ryzen 5 4500U，go1.27.1）。而 freedom / socks / trojan
// 的 UDP 路径**每个包**都要序列化一次地址，前两处手里本来就有可复用的裸切片，
// 走 AppendTo 就是零额外分配。
//
// 出错时返回的切片可能已经追加了部分内容，调用方必须整个丢弃。这也是与旧 WriteTo
// 的一处**可观察差异**：旧实现会把已经写进 w 的 ATYP 字节留在流里再报错（只有直接调
// Address.WriteTo 才有这个问题，Metadata.WriteTo 本来就攒在内部缓冲里、失败即丢弃）。
// 全仓调用点都不看返回的字节数、且拿到 error 就等于放弃这个包/这条连接，所以统一成
// "要么全写、要么一个字都不写"；域名超长或 ATYP 非法时不再往流里留半截垃圾字节。
func (a *Address) AppendTo(buf []byte) ([]byte, error) {
	buf = append(buf, byte(a.AddressType))
	switch a.AddressType {
	case DomainName:
		if len(a.DomainName) > 255 {
			return buf, common.NewError("domain name too long for serialization")
		}
		buf = append(buf, byte(len(a.DomainName)))
		buf = append(buf, a.DomainName...)
	case IPv4:
		// To4() 对纯 IPv6 返回 nil：旧实现是 w.Write(nil)，写 0 字节且不报错，
		// append 空切片是同一件事——"ATYP 说是 v4 却只有 3 字节"这种畸形输出的形态没变，
		// 判错仍然是调用方的责任，这里不趁重构顺手改语义
		buf = append(buf, a.IP.To4()...)
	case IPv6:
		buf = append(buf, a.IP.To16()...)
	default:
		return buf, common.NewError("invalid ATYP " + strconv.FormatInt(int64(a.AddressType), 10))
	}
	portVal, e := common.SafeUint16FromInt(a.Port)
	if e != nil {
		return buf, common.NewError("invalid port number: out of range").Base(e)
	}
	var port [2]byte
	binary.BigEndian.PutUint16(port[:], portVal)
	buf = append(buf, port[:]...)
	return buf, nil
}

// wireLength 是这个地址序列化后会占多少字节，只用来给 WriteTo 预分配容量。
//
// 它和 AppendTo 是两处代码，但**不需要**被同一组门禁钉住：这里算错只会多一次 append
// 扩容（性能），改不了任何一个输出字节（正确性）。IPv4 分支按满 4 字节算，若 IP 其实
// 是纯 IPv6，To4() 返回 nil、实际只写 3 字节，那就是多预留 4 字节，同样无害。
func (a *Address) wireLength() int {
	switch a.AddressType {
	case DomainName:
		return 1 + 1 + len(a.DomainName) + 2 // ATYP + 长度字节 + 域名 + 端口
	case IPv4:
		return 1 + 4 + 2
	case IPv6:
		return 1 + 16 + 2
	default:
		return 1 + 2
	}
}

func (a *Address) WriteTo(w io.Writer) (int64, error) {
	buf, err := a.AppendTo(make([]byte, 0, a.wireLength()))
	if err != nil {
		return 0, err
	}
	n, err := w.Write(buf)
	return int64(n), err
}
