package websocket

import (
	"net"
	"context"

	"github.com/Potterli20/trojan-go-fork/tunnel"
	"golang.org/x/net/websocket"
)

type OutboundConn struct {
	Conn    *websocket.Conn
	tcpConn tunnel.Conn
}

// LocalAddr returns the local network address.
func (oc *OutboundConn) LocalAddr() net.Addr {
	return oc.tcpConn.LocalAddr()
}

// Metadata returns metadata associated with the connection.
func (oc *OutboundConn) Metadata() *tunnel.Metadata { // Change the return type to *tunnel.Metadata
	// Implement Metadata method if needed.
	return nil
}

func (c *OutboundConn) RemoteAddr() net.Addr {
	// override RemoteAddr of websocket.Conn, or it will return some URL from "Origin"
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
