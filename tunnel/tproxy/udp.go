//go:build linux

package tproxy

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

// ipv6RecvOrigDstAddr 即 IPV6_RECVORIGDSTADDR(74)：stdlib 的 syscall 包仅在部分
// 架构(如 loong64)导出该常量，amd64/arm64 缺失；该值在 Linux 各架构上一致
const ipv6RecvOrigDstAddr = 0x4a

// ListenUDP will construct a new UDP listener
// socket with the Linux IP_TRANSPARENT option
// set on the underlying socket
func ListenUDP(network string, laddr *net.UDPAddr) (*net.UDPConn, error) {
	listener, err := net.ListenUDP(network, laddr)
	if err != nil {
		return nil, err
	}

	fileDescriptorSource, err := listener.File()
	if err != nil {
		return nil, &net.OpError{Op: "listen", Net: network, Source: nil, Addr: laddr, Err: fmt.Errorf("get file descriptor: %s", err)}
	}
	defer fileDescriptorSource.Close()

	fileDescriptor := int(fileDescriptorSource.Fd())
	if err = syscall.SetsockoptInt(fileDescriptor, syscall.SOL_IP, syscall.IP_TRANSPARENT, 1); err != nil {
		return nil, &net.OpError{Op: "listen", Net: network, Source: nil, Addr: laddr, Err: fmt.Errorf("set socket option: IP_TRANSPARENT: %s", err)}
	}

	// 双栈("udp")或 udp4 socket 设置 v4 选项；IPv6 socket(udp6 或 "udp" 绑定
	// v6 地址/未指定)还需设置 v6 层选项，否则内核不投递 original dst cmsg，
	// tproxy 无法取回真实目的地。绑定 v4 地址的 "udp" 实际是 AF_INET socket，
	// 对其设置 SOL_IPV6 层选项会 ENOPROTOOPT，故按 LocalAddr 的实际 family 判断
	if err = syscall.SetsockoptInt(fileDescriptor, syscall.SOL_IP, syscall.IP_RECVORIGDSTADDR, 1); err != nil {
		return nil, &net.OpError{Op: "listen", Net: network, Source: nil, Addr: laddr, Err: fmt.Errorf("set socket option: IP_RECVORIGDSTADDR: %s", err)}
	}
	if localAddr, ok := listener.LocalAddr().(*net.UDPAddr); ok && (localAddr.IP == nil || localAddr.IP.To4() == nil) {
		if err = syscall.SetsockoptInt(fileDescriptor, syscall.SOL_IPV6, ipv6RecvOrigDstAddr, 1); err != nil {
			return nil, &net.OpError{Op: "listen", Net: network, Source: nil, Addr: laddr, Err: fmt.Errorf("set socket option: IPV6_RECVORIGDSTADDR: %s", err)}
		}
	}

	return listener, nil
}

// ReadFromUDP reads a UDP packet from c, copying the payload into b.
// It returns the number of bytes copied into b and the return address
// that was on the packet.
//
// Out-of-band data is also read in so that the original destination
// address can be identified and parsed.
func ReadFromUDP(conn *net.UDPConn, b []byte) (int, *net.UDPAddr, *net.UDPAddr, error) {
	oob := make([]byte, 1024)
	n, oobn, _, addr, err := conn.ReadMsgUDP(b, oob)
	if err != nil {
		return 0, nil, nil, err
	}

	msgs, err := syscall.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		return 0, nil, nil, fmt.Errorf("parsing socket control message: %s", err)
	}

	var originalDst *net.UDPAddr
	for _, msg := range msgs {
		// v4 cmsg 是 (SOL_IP, IP_RECVORIGDSTADDR)，v6 是 (SOL_IPV6, IPV6_RECVORIGDSTADDR)；
		// 旧代码用 v4 的 Type(20) 同时匹配两层，导致 IPv6 包永远取不到 originalDst
		switch {
		case msg.Header.Level == syscall.SOL_IP && msg.Header.Type == syscall.IP_RECVORIGDSTADDR:
			if len(msg.Data) < int(unsafe.Sizeof(syscall.RawSockaddrInet4{})) {
				return 0, nil, nil, fmt.Errorf("truncated IPv4 original destination address")
			}
			pp := (*syscall.RawSockaddrInet4)(unsafe.Pointer(&msg.Data[0]))
			p := (*[2]byte)(unsafe.Pointer(&pp.Port))
			originalDst = &net.UDPAddr{
				IP:   net.IPv4(pp.Addr[0], pp.Addr[1], pp.Addr[2], pp.Addr[3]),
				Port: int(p[0])<<8 + int(p[1]),
			}

		case msg.Header.Level == syscall.SOL_IPV6 && msg.Header.Type == ipv6RecvOrigDstAddr:
			if len(msg.Data) < int(unsafe.Sizeof(syscall.RawSockaddrInet6{})) {
				return 0, nil, nil, fmt.Errorf("truncated IPv6 original destination address")
			}
			pp := (*syscall.RawSockaddrInet6)(unsafe.Pointer(&msg.Data[0]))
			p := (*[2]byte)(unsafe.Pointer(&pp.Port))
			originalDst = &net.UDPAddr{
				IP:   net.IP(pp.Addr[:]),
				Port: int(p[0])<<8 + int(p[1]),
				Zone: strconv.Itoa(int(pp.Scope_id)),
			}
		}
	}

	if originalDst == nil {
		return 0, nil, nil, fmt.Errorf("unable to obtain original destination: no IP_RECVORIGDSTADDR control message received")
	}

	return n, addr, originalDst, nil
}

// DialUDP connects to the remote address raddr on the network net,
// which must be "udp", "udp4", or "udp6".  If laddr is not nil, it is
// used as the local address for the connection.
func DialUDP(network string, laddr *net.UDPAddr, raddr *net.UDPAddr) (*net.UDPConn, error) {
	remoteSocketAddress, err := udpAddrToSocketAddr(raddr)
	if err != nil {
		return nil, &net.OpError{Op: "dial", Err: fmt.Errorf("build destination socket address: %s", err)}
	}

	localSocketAddress, err := udpAddrToSocketAddr(laddr)
	if err != nil {
		return nil, &net.OpError{Op: "dial", Err: fmt.Errorf("build local socket address: %s", err)}
	}

	fileDescriptor, err := syscall.Socket(udpAddrFamily(network, laddr, raddr), syscall.SOCK_DGRAM, 0)
	if err != nil {
		return nil, &net.OpError{Op: "dial", Err: fmt.Errorf("socket open: %s", err)}
	}

	if err = syscall.SetsockoptInt(fileDescriptor, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
		syscall.Close(fileDescriptor)
		return nil, &net.OpError{Op: "dial", Err: fmt.Errorf("set socket option: SO_REUSEADDR: %s", err)}
	}

	if err = syscall.SetsockoptInt(fileDescriptor, syscall.SOL_IP, syscall.IP_TRANSPARENT, 1); err != nil {
		syscall.Close(fileDescriptor)
		return nil, &net.OpError{Op: "dial", Err: fmt.Errorf("set socket option: IP_TRANSPARENT: %s", err)}
	}

	if err = syscall.Bind(fileDescriptor, localSocketAddress); err != nil {
		syscall.Close(fileDescriptor)
		return nil, &net.OpError{Op: "dial", Err: fmt.Errorf("socket bind: %s", err)}
	}

	if err = syscall.Connect(fileDescriptor, remoteSocketAddress); err != nil {
		syscall.Close(fileDescriptor)
		return nil, &net.OpError{Op: "dial", Err: fmt.Errorf("socket connect: %s", err)}
	}

	fdFile := os.NewFile(uintptr(fileDescriptor), fmt.Sprintf("net-udp-dial-%s", raddr.String()))
	defer fdFile.Close()

	remoteConn, err := net.FileConn(fdFile)
	if err != nil {
		// fd 的关闭由上面的 defer fdFile.Close() 负责，这里再 syscall.Close 会 double-close
		return nil, &net.OpError{Op: "dial", Err: fmt.Errorf("convert file descriptor to connection: %s", err)}
	}

	return remoteConn.(*net.UDPConn), nil
}

// udpAddToSockerAddr will convert a UDPAddr
// into a Sockaddr that may be used when
// connecting and binding sockets
func udpAddrToSocketAddr(addr *net.UDPAddr) (syscall.Sockaddr, error) {
	switch {
	case addr.IP.To4() != nil:
		ip := [4]byte{}
		copy(ip[:], addr.IP.To4())

		return &syscall.SockaddrInet4{Addr: ip, Port: addr.Port}, nil

	default:
		ip := [16]byte{}
		copy(ip[:], addr.IP.To16())

		// Zone 缺失(全球单播地址常态)或无法解析时按 0 处理：
		// ParseUint("") 会报错导致整个回包拨号失败
		zoneID, err := strconv.ParseUint(addr.Zone, 10, 32)
		if err != nil {
			zoneID = 0
		}

		return &syscall.SockaddrInet6{Addr: ip, Port: addr.Port, ZoneId: uint32(zoneID)}, nil
	}
}

// udpAddrFamily will attempt to work
// out the address family based on the
// network and UDP addresses
func udpAddrFamily(net string, laddr, raddr *net.UDPAddr) int {
	switch net[len(net)-1] {
	case '4':
		return syscall.AF_INET
	case '6':
		return syscall.AF_INET6
	}

	if (laddr == nil || laddr.IP.To4() != nil) &&
		(raddr == nil || raddr.IP.To4() != nil) {
		return syscall.AF_INET
	}
	return syscall.AF_INET6
}
