package trojan

import (
	"bytes"
	"encoding/binary"
	"io"
	"io/ioutil"
	"net"

	"github.com/Potterli20/trojan-go/common"
	"github.com/Potterli20/trojan-go/log"
	"github.com/Potterli20/trojan-go/recorder"
	"github.com/Potterli20/trojan-go/tunnel"
	"github.com/Potterli20/trojan-go/tunnel/mux"
)

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
	packet := make([]byte, 0, MaxPacketSize)
	w := bytes.NewBuffer(packet)
	metadata.Address.WriteTo(w)

	length := len(payload)
	lengthBuf := [2]byte{}
	crlf := [2]byte{0x0d, 0x0a}

	binary.BigEndian.PutUint16(lengthBuf[:], uint16(length))
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

	if err := addr.ReadFrom(c.Conn); err != nil {
		c.Conn.Close()
		return 0, nil, common.NewError("failed to parse udp packet addr").Base(err)
	}
	lengthBuf := [2]byte{}
	if _, err := io.ReadFull(c.Conn, lengthBuf[:]); err != nil {
		return 0, nil, common.NewError("failed to read length")
	}
	length := int(binary.BigEndian.Uint16(lengthBuf[:]))

	crlf := [2]byte{}
	if _, err := io.ReadFull(c.Conn, crlf[:]); err != nil {
		return 0, nil, common.NewError("failed to read crlf")
	}

	if len(payload) < length || length > MaxPacketSize {
		io.CopyN(ioutil.Discard, c.Conn, int64(length)) // drain the rest of the packet
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
