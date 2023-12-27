package websocket

import (
	"context"
	"net"

	"github.com/Potterli20/trojan-go/tunnel"
	"golang.org/x/net/websocket"
)

type OutboundConn struct {
	*websocket.Conn
	tcpConn net.Conn
}

func (c *OutboundConn) Metadata() *tunnel.Metadata {
	return nil
}

func (c *OutboundConn) RemoteAddr() net.Addr {
	// override RemoteAddr of websocket.Conn, or it will return some url from "Origin"
	return c.tcpConn.RemoteAddr()
}

type InboundConn struct {
	OutboundConn
	ctx    context.Context
	cancel context.CancelFunc
}

func (c *InboundConn) Close() error {
	c.cancel()
	return c.Conn.Close()
}
