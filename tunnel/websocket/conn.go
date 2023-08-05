package websocket

import (
	"context"
	"net"
	"net/http"

	"github.com/quic-go/quic-go/http3"
	"golang.org/x/net/http2"
	"golang.org/x/net/websocket"

	"github.com/Potterli20/trojan-go-fork/tunnel"
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
