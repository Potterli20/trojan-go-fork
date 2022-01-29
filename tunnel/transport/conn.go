package transport

import (
	"net"

	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type Conn struct {
	net.Conn
}

func (c *Conn) Metadata() *tunnel.Metadata {
	return nil
}
