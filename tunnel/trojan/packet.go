package trojan

import (
	"bytes"
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

// bufPool 复用 bytes.Buffer，减少 UDP 包写入时的内存分配
var bufPool = sync.Pool{
	New: func() any {
		return bytes.NewBuffer(make([]byte, 0, MaxPacketSize))
	},
}

type PacketConn struct {
	tunnel.Conn
}

func (c *PacketConn) ReadFrom(payload []byte) (int, net.Addr, error) {
	return c.ReadWithMetadata(payload)
}

func (c *PacketConn) WriteTo(payload []byte, addr net.Addr) (int, error) {
	address, err := tunnel.NewAddressFromAddr("udp", addr.String())
	if err != nil {
		return 0, err
	}
	m := &tunnel.Metadata{
		Address: address,
	}
	return c.WriteWithMetadata(payload, m)
}

func (c *PacketConn) WriteWithMetadata(payload []byte, metadata *tunnel.Metadata) (int, error) {
	w := bufPool.Get().(*bytes.Buffer)
	w.Reset()
	defer bufPool.Put(w)

	metadata.Address.WriteTo(w) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理

	length := len(payload)
	lengthBuf := [2]byte{}
	crlf := [2]byte{0x0d, 0x0a}

	if length > MaxPacketSize || length < 0 {
		return 0, common.NewError("invalid packet length for serialization")
	}
	binary.BigEndian.PutUint16(lengthBuf[:], uint16(length)) //gosec:disable -- 上方已校验 length 在 [0, MaxPacketSize] 范围内
	w.Write(lengthBuf[:])
	w.Write(crlf[:])
	w.Write(payload)

	_, err := c.Conn.Write(w.Bytes())

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
		io.CopyN(io.Discard, c.Conn, int64(length)) // drain the rest of the packet //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
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
