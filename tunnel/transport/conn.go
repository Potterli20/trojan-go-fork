package transport

import (
	"net"

	"gitlab.atcatw.org/atca/community-edition/trojan-go.git/tunnel"
)

type Conn struct {
	net.Conn
}

func (c *Conn) Metadata() *tunnel.Metadata {
	return nil
}
