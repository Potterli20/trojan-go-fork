package websocket

import (
	"context"
	"net"

	"github.com/Potterli20/trojan-go-fork/tunnel"
	"golang.org/x/net/websocket"
)

type OutboundConn struct {
	*websocket.Conn
	tcpConn net.Conn
}

type OutboundConn struct {
	Conn    *websocket.Conn
	tcpConn tunnel.Conn
}

func (oc *OutboundConn) LocalAddr() net.Addr {
	return oc.tcpConn.LocalAddr()
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
