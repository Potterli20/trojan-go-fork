package websocket

import (
	"net"
	"time"

	"github.com/Potterli20/trojan-go-fork/tunnel"
	"golang.org/x/net/websocket"
)


// OutboundConn represents a WebSocket connection.
type OutboundConn struct {
	websocket *websocket.Conn
}

// SetReadDeadline sets the read deadline on the connection.
func (c *OutboundConn) SetReadDeadline(t time.Time) error {
	return c.websocket.SetReadDeadline(t)
}

// Read implements the Read method of the tunnel.Conn interface.
func (c *OutboundConn) Read(b []byte) (n int, err error) {
	return c.websocket.Read(b)
}

// Write implements the Write method of the tunnel.Conn interface.
func (c *OutboundConn) Write(b []byte) (n int, err error) {
	return c.websocket.Write(b)
}

// Close implements the Close method of the tunnel.Conn interface.
func (c *OutboundConn) Close() error {
	return c.websocket.Close()
}
