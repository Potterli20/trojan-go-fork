package tunnel

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"

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
	buf := bytes.NewBuffer(make([]byte, 0, 64))
	buf.WriteByte(byte(r.Command))
	_, err := r.Address.WriteTo(buf)
	if err != nil {
		return 0, err
	}
	// use tcp by default
	r.Address.NetworkType = "tcp"
	n, err := buf.WriteTo(w)
	if err != nil {
		return n, err
	}
	return n, nil
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
		return a.IP, nil
	}
	log.Debug("ResolveIP Start! domain: ", a.DomainName, " PORT: ", a.Port)
	ipAddr, err := net.ResolveIPAddr("ip", a.DomainName)
	if err != nil {
		return nil, err
	}
	log.Debug("ResolveIP Done! IP: ", ipAddr.IP)
	a.IP = ipAddr.IP
	return ipAddr.IP, nil
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

func (a *Address) WriteTo(w io.Writer) (int64, error) {
	n, err := w.Write([]byte{byte(a.AddressType)})
	if err != nil {
		return int64(n), err
	}
	total := int64(n)
	switch a.AddressType {
	case DomainName:
		if len(a.DomainName) > 255 {
			return total, common.NewError("domain name too long for serialization")
		}
		n, err := w.Write([]byte{byte(len(a.DomainName))}) //gosec:disable -- 上方已校验长度 <= 255
		total += int64(n)
		if err != nil {
			return total, err
		}
		n, err = w.Write([]byte(a.DomainName))
		total += int64(n)
		if err != nil {
			return total, err
		}
	case IPv4:
		n, err = w.Write(a.IP.To4())
		total += int64(n)
	case IPv6:
		n, err = w.Write(a.IP.To16())
		total += int64(n)
	default:
		return total, common.NewError("invalid ATYP " + strconv.FormatInt(int64(a.AddressType), 10))
	}
	if err != nil {
		return total, err
	}
	port := [2]byte{}
	portVal, e := common.SafeUint16FromInt(a.Port)
	if e != nil {
		return total, common.NewError("invalid port number: out of range").Base(e)
	}
	binary.BigEndian.PutUint16(port[:], portVal)
	n, err = w.Write(port[:])
	total += int64(n)
	return total, err
}
