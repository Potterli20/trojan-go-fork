package freedom

import (
	"bytes"
	"net"
	"sync"

	"github.com/Potterli20/socks5-fork"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

const MaxPacketSize = 1024 * 8

// socksPacketBufPool 复用 SOCKS5 UDP 中继的每包缓冲：两个方向此前每搬一个包
// 就 make 一次 8KB，分配次数与包数同阶。这里的 buffer 不逃出函数作用域，
// 所以归还点就在同一个函数里，不需要跨 goroutine 的借用协议。
var socksPacketBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, MaxPacketSize)
		return &buf
	},
}

func getSocksPacketBuf() []byte { return *socksPacketBufPool.Get().(*[]byte) }

func putSocksPacketBuf(buf []byte) {
	if cap(buf) != MaxPacketSize {
		return
	}
	// 借出方可能拿着 buf[:n] 归还，必须还原成全长再入池
	whole := buf[:cap(buf)]
	socksPacketBufPool.Put(&whole)
}

type Conn struct {
	net.Conn
}

func (c *Conn) Metadata() *tunnel.Metadata {
	return nil
}

type PacketConn struct {
	*net.UDPConn
}

func (c *PacketConn) WriteWithMetadata(p []byte, m *tunnel.Metadata) (int, error) {
	return c.WriteTo(p, m.Address)
}

func (c *PacketConn) ReadWithMetadata(p []byte) (int, *tunnel.Metadata, error) {
	n, addr, err := c.ReadFrom(p)
	if err != nil {
		return 0, nil, err
	}
	address, err := tunnel.NewAddressFromAddr("udp", addr.String())
	if err != nil {
		return 0, nil, common.NewError("freedom failed to parse packet address").Base(err)
	}
	metadata := &tunnel.Metadata{
		Address: address,
	}
	return n, metadata, nil
}

func (c *PacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if udpAddr, ok := addr.(*net.UDPAddr); ok {
		return c.WriteToUDP(p, udpAddr)
	}
	ip, err := addr.(*tunnel.Address).ResolveIP()
	if err != nil {
		return 0, err
	}
	udpAddr := &net.UDPAddr{
		IP:   ip,
		Port: addr.(*tunnel.Address).Port,
	}
	return c.WriteToUDP(p, udpAddr)
}

type SocksPacketConn struct {
	net.PacketConn
	socksAddr   *net.UDPAddr
	socksClient *socks5.Client
}

func (c *SocksPacketConn) WriteWithMetadata(payload []byte, metadata *tunnel.Metadata) (int, error) {
	scratch := getSocksPacketBuf()
	defer putSocksPacketBuf(scratch)
	buf := bytes.NewBuffer(scratch[:0])
	buf.Write([]byte{0, 0, 0}) // RSV, FRAG
	_, err := metadata.Address.WriteTo(buf)
	if err != nil {
		return 0, common.NewError("freedom failed to write socks address").Base(err)
	}
	buf.Write(payload)
	_, err = c.PacketConn.WriteTo(buf.Bytes(), c.socksAddr)
	if err != nil {
		return 0, err
	}
	log.Debug("sent udp packet to " + c.socksAddr.String() + " with metadata " + metadata.String())
	return len(payload), nil
}

func (c *SocksPacketConn) ReadWithMetadata(payload []byte) (int, *tunnel.Metadata, error) {
	buf := getSocksPacketBuf()
	defer putSocksPacketBuf(buf)
	n, from, err := c.PacketConn.ReadFrom(buf)
	if err != nil {
		return 0, nil, err
	}
	log.Debug("recv udp packet from " + from.String())
	addr := new(tunnel.Address)
	r := bytes.NewBuffer(buf[3:n])
	_, err = addr.ReadFrom(r)
	if err != nil {
		return 0, nil, common.NewError("socks5 failed to parse addr in the packet").Base(err)
	}
	length, err := r.Read(payload)
	if err != nil {
		return 0, nil, err
	}
	return length, &tunnel.Metadata{
		Address: addr,
	}, nil
}

func (c *SocksPacketConn) Close() error {
	c.socksClient.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	return c.PacketConn.Close()
}
