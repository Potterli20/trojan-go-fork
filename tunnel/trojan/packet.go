package trojan

import (
	"encoding/binary"
	"io"
	"net"
	"sync"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/recorder"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/Potterli20/trojan-go-fork/tunnel/mux"
)

// packetBufCap 是池化帧缓冲的预留容量。一个 datagram 最多需要
// 「ATYP(1) + 域名长度字节(1) + 域名(255) + 端口(2)」+ 长度(2) + CRLF(2) + 负载(MaxPacketSize)
// = MaxPacketSize + 263，这里取整到 +264。
//
// 预留低于这个口径就会在满尺寸包上触发 growslice，而下面的归还闸门是按 cap 判定的：
// 被撑到 16KB 的对象永远进不了池，等于池对满尺寸 UDP 包彻底失效——每包新建 8KB、
// 丢弃 16KB，分配次数回到与包数同阶，还顺手给 GC 加压。
const packetBufCap = MaxPacketSize + 264

// bufPool 复用 UDP 帧缓冲，池里放的是切片指针而不是 bytes.Buffer：
// 帧要交给 Address.AppendTo 就地拼装，用 bytes.Buffer 还得先攒进一个临时切片再 Write，
// 每包多一次分配（口径对照 tunnel/router 的 packetBufPool）。
var bufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 0, packetBufCap)
		return &buf
	},
}

type PacketConn struct {
	tunnel.Conn
}

func (c *PacketConn) ReadFrom(payload []byte) (int, net.Addr, error) {
	return c.ReadWithMetadata(payload)
}

func (c *PacketConn) WriteTo(payload []byte, addr net.Addr) (int, error) {
	// 每包一次：trojan 的 UDP 是封在 TCP 流里的 datagram，这里是出站方向的地址组装
	address, err := tunnel.NewAddressFromNetAddr("udp", addr)
	if err != nil {
		return 0, err
	}
	m := &tunnel.Metadata{
		Address: address,
	}
	return c.WriteWithMetadata(payload, m)
}

func (c *PacketConn) WriteWithMetadata(payload []byte, metadata *tunnel.Metadata) (int, error) {
	// 空地址必须在这里挡住：Address.AppendTo/WriteTo 都不做 nil 保护，旧写法直接
	// 解引用崩掉中继协程，而全仓确有产出空地址 metadata 的点（tunnel/quic/client.go:283,290）
	if metadata == nil || metadata.Address == nil {
		return 0, common.NewError("trojan udp packet without a destination address")
	}

	length := len(payload)
	if length > MaxPacketSize || length < 0 {
		return 0, common.NewError("invalid packet length for serialization")
	}

	bufp := bufPool.Get().(*[]byte)
	frame := (*bufp)[:0]
	// 归还前必须还原成全长，否则下一个 Get 拿到的是被 append 削短过的 slice；
	// 超出预留容量（说明中途扩过容）的对象直接丢给 GC，不让池的水位被顶高
	defer func() {
		if cap(frame) <= packetBufCap {
			*bufp = frame[:cap(frame)]
			bufPool.Put(bufp)
		}
	}()

	// 先攒完整帧、确认序列化成功再写。旧写法丢掉 WriteTo 的错误继续往下发，于是流里
	// 出现一个"没有地址的 datagram"（只有 length + CRLF + 负载），对端按 trojan 的
	// datagram 状态机解就永久错位——整条 UDP 中继静默报废。
	var err error
	if frame, err = metadata.Address.AppendTo(frame); err != nil {
		return 0, common.NewError("trojan failed to serialize udp address").Base(err)
	}

	var lengthBuf [2]byte
	binary.BigEndian.PutUint16(lengthBuf[:], uint16(length)) //gosec:disable -- 上方已校验 length 在 [0, MaxPacketSize] 范围内
	frame = append(frame, lengthBuf[:]...)
	frame = append(frame, '\r', '\n')
	frame = append(frame, payload...)

	_, err = c.Conn.Write(frame)

	log.Debug("udp packet remote", c.RemoteAddr(), "metadata", metadata, "size", length)
	c.Record(metadata, payload)
	return len(payload), err
}

func (c *PacketConn) ReadWithMetadata(payload []byte) (int, *tunnel.Metadata, error) {
	addr := &tunnel.Address{
		NetworkType: "udp",
	}

	_, err := addr.ReadFrom(c.Conn)
	if err != nil {
		c.Conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		return 0, nil, common.NewError("failed to parse udp packet addr").Base(err)
	}
	lengthBuf := [2]byte{}
	if _, err := io.ReadFull(c.Conn, lengthBuf[:]); err != nil {
		return 0, nil, common.NewError("failed to read length")
	}
	length := common.SafeIntFromUint16(binary.BigEndian.Uint16(lengthBuf[:]))

	crlf := [2]byte{}
	if _, err := io.ReadFull(c.Conn, crlf[:]); err != nil {
		return 0, nil, common.NewError("failed to read crlf")
	}

	if len(payload) < length || length > MaxPacketSize {
		// 先把这个超长包排干净再报错，否则残字节会留在流里让后续每一个包错位。
		// 不用 io.CopyN：Go 1.27 的 io.copyBuffer 在 buf==nil 时直接 make([]byte, 32*1024)，
		// 而这里的 length 来自**对端写的 2 字节长度**（最大 65535），远端只要持续发超长包
		// 就能按包放大本端分配。这里复用调用方自己的缓冲分片读掉，零额外分配。
		remaining := length
		for remaining > 0 && len(payload) > 0 {
			chunk := len(payload)
			if chunk > remaining {
				chunk = remaining
			}
			if _, err := io.ReadFull(c.Conn, payload[:chunk]); err != nil {
				break //gosec:disable -- 错误忽略：排包路径，错误由下方统一返回
			}
			remaining -= chunk
		}
		return 0, nil, common.NewError("incoming packet size is too large")
	}

	if _, err := io.ReadFull(c.Conn, payload[:length]); err != nil {
		return 0, nil, common.NewError("failed to read payload")
	}

	log.Debug("udp packet from", c.RemoteAddr(), "metadata", addr.String(), "size", length)
	c.Record(addr, payload[:length])
	return length, &tunnel.Metadata{
		Address: addr,
	}, nil
}

func (c *PacketConn) getUserHash() string {
	switch c.Conn.(type) {
	case *InboundConn:
		trojanConn := c.Conn.(*InboundConn)
		return trojanConn.Hash()
	case *mux.Conn:
		muxConn := c.Conn.(*mux.Conn)
		if trojanConn, ok := muxConn.Conn.(*InboundConn); ok {
			return trojanConn.Hash()
		}
	}
	return ""
}

func (c *PacketConn) Record(addr net.Addr, payload []byte) {
	userHash := c.getUserHash()
	if userHash == "" {
		return
	}
	log.Debug("user", userHash, "from", c.RemoteAddr(), "tunneling UDP to", addr)
	recorder.Add(userHash, c.RemoteAddr(), addr, "UDP", payload)
}
