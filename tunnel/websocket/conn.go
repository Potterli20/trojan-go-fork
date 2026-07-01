package websocket

import (
	"context"
	"net"

	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
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
	return c.tcpConn.RemoteAddr()
}

func (c *OutboundConn) Close() error {
	if c.tcpConn != nil {
		c.tcpConn.Close()
	}
	if c.Conn != nil {
		return c.Conn.Close()
	}
	return nil
}

type InboundConn struct {
	OutboundConn
	ctx     context.Context
	cancel  context.CancelFunc
	tracker *log.ConnectionTracker
}

func (c *InboundConn) Close() error {
	c.cancel()
	err := c.Conn.Close()
	if c.tcpConn != nil {
		c.tcpConn.Close()
	}
	return err
}
