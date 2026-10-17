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

// socksFrameBufCap 是每包缓冲的预留容量。写出方向要在同一块切片上就地攒
// 「RSV+FRAG(3) + 地址块(ATYP(1)+长度字节(1)+域名(255)+端口(2)=259) + 负载(≤MaxPacketSize)」，
// 取整到 +264。只留 MaxPacketSize 的话满尺寸负载必然把 append 撑到界外：池里那块永远
// 停在 8192，每包另外新造一份 10880（实测的 Go size class），池化省下的分配又被加了
// 回来（远端按线上格式合法发满尺寸 datagram 就能逐包触发）。口径同 tunnel/trojan/packet.go
// 的 packetBufCap。
const socksFrameBufCap = MaxPacketSize + 264

// socksPacketBufPool 复用 SOCKS5 UDP 中继的每包缓冲：两个方向此前每搬一个包
// 就 make 一次 8KB，分配次数与包数同阶。这里的 buffer 不逃出函数作用域，
// 所以归还点就在同一个函数里，不需要跨 goroutine 的借用协议。
var socksPacketBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, socksFrameBufCap)
		return &buf
	},
}

func getSocksPacketBuf() []byte { return *socksPacketBufPool.Get().(*[]byte) }

func putSocksPacketBuf(buf []byte) {
	if cap(buf) != socksFrameBufCap {
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
	// 每包一次的地址组装：底层就是 *net.UDPAddr，不必再走一遍字符串往返
	address, err := tunnel.NewAddressFromNetAddr("udp", addr)
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
	// 就地拼在池化切片上：Address.WriteTo 要为一次序列化额外分配临时切片，而这是每包一次
	out := append(scratch[:0], 0, 0, 0) // RSV, FRAG
	out, err := metadata.Address.AppendTo(out)
	if err != nil {
		return 0, common.NewError("freedom failed to write socks address").Base(err)
	}
	out = append(out, payload...)
	_, err = c.PacketConn.WriteTo(out, c.socksAddr)
	if err != nil {
		return 0, err
	}
	// 先过级别门禁再拼字符串：这是每 UDP 包一次的路径，而 log.Debug 的门禁在函数内部，
	// 参数在那之前就已经算完了（口径见 AGENTS.md 的日志约定）
	if log.ShouldLog(log.DebugLevel) {
		log.Debug("sent udp packet to " + c.socksAddr.String() + " with metadata " + metadata.String())
	}
	return len(payload), nil
}

func (c *SocksPacketConn) ReadWithMetadata(payload []byte) (int, *tunnel.Metadata, error) {
	buf := getSocksPacketBuf()
	defer putSocksPacketBuf(buf)
	// 读侧只用前 MaxPacketSize 字节：缓冲为写出方向的组帧留了 headroom，这里不能顺带
	// 把它抬高，否则内核在这一跳截断的语义就变了（见 socksFrameBufCap）。
	n, from, err := c.PacketConn.ReadFrom(buf[:MaxPacketSize])
	if err != nil {
		return 0, nil, err
	}
	if log.ShouldLog(log.DebugLevel) {
		log.Debug("recv udp packet from " + from.String())
	}
	addr := new(tunnel.Address)
	// 分片头固定 3 字节，其后至少还要有 1 字节 ATYP 才谈得上一个合法地址字段。
	// 短于此必须先拒掉：buf[3:n] 在 n<3 时直接切片越界 panic，而这一层的对端是
	// UDP——任何能往这个 bind 端口投包的人都能把整个进程崩掉，relay 这条链上没有
	// recover（只有 mux/tls/trojan 三处有）。
	if n < 4 {
		return 0, nil, common.NewError("socks5 udp packet shorter than the fragment header")
	}
	// 注意这里的别名关系：r 直接持有**池化 buf** 的切片，而上面 defer 会在本函数返回时
	// 把它归还进池。之所以安全，是因为 Address.ReadFrom 只把字节读进本地数组、域名
	// 走 string() 复制，解析结果不引用 buf；改成"直接持有底层切片的一段"就会跨包串数据。
	// 这条不变量由 TestParsedAddressDoesNotAliasPooledBuffer 钉住（涂脏归还后的 buf，
	// 断言已返回的地址与负载不变），改 ReadFrom 前先想清楚它。
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
