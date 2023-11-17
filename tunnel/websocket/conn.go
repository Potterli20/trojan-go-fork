package websocket

import (
	"net"
	"time"

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
func (oc *OutboundConn) Metadata() *tunnel.Metadata {
	// Implement Metadata method if needed.
	return nil
}

// Read implements the io.Reader interface.
func (oc *OutboundConn) Read(b []byte) (int, error) {
	// Implement the Read method using the underlying websocket.Conn or tcpConn.
	// Example:
	// return oc.tcpConn.Read(b)
	return 0, nil
}

// Close implements the io.Closer interface.
func (oc *OutboundConn) Close() error {
	// Implement the Close method using the underlying websocket.Conn or tcpConn.
	// Example:
	// return oc.tcpConn.Close()
	return nil
}

// SetDeadline sets the read and write deadlines for the connection.
func (oc *OutboundConn) SetDeadline(t time.Time) error {
	// Implement the SetDeadline method using the underlying websocket.Conn or tcpConn.
	// Example:
	// return oc.tcpConn.SetDeadline(t)
	return nil
}

func (c *OutboundConn) RemoteAddr() net.Addr {
	// override RemoteAddr of websocket.Conn, or it will return some URL from "Origin"
	return c.tcpConn.RemoteAddr()
}
