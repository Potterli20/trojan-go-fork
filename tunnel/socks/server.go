package socks

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

const (
	Connect   tunnel.Command = 1
	Associate tunnel.Command = 3
)

const (
	MaxPacketSize = 1024 * 8
)

type Server struct {
	connChan         chan tunnel.Conn
	packetChan       chan tunnel.PacketConn
	underlay         tunnel.Server
	localHost        string
	localPort        int
	timeout          time.Duration
	listenPacketConn tunnel.PacketConn
	mapping          map[string]*PacketConn
	mappingLock      sync.RWMutex
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup
}

func (s *Server) AcceptConn(tunnel.Tunnel) (tunnel.Conn, error) {
	select {
	case conn := <-s.connChan:
		return conn, nil
	case <-s.ctx.Done():
		return nil, common.NewError("socks server closed")
	}
}

func (s *Server) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	select {
	case conn := <-s.packetChan:
		return conn, nil
	case <-s.ctx.Done():
		return nil, common.NewError("socks server closed")
	}
}

func (s *Server) acceptConnLoop() {
	for {
		conn, err := s.underlay.AcceptConn(&Tunnel{})
		if err != nil {
			select {
			case <-s.ctx.Done():
				log.Debug("exiting")
				return
			default:
				log.Error(common.NewError("socks failed to accept conn").Base(err))
				continue
			}
		}
		go func(conn tunnel.Conn) {
			defer conn.Close()
			conn, err := s.handshake(conn)
			if err != nil {
				log.Error(common.NewError("socks failed to handshake").Base(err))
				return
			}
			switch conn.Metadata().Command {
			case Connect:
				log.Info("socks connect request from", conn.RemoteAddr(), "metadata", conn.Metadata())
				err = s.connect(conn)
				if err != nil {
					log.Error(common.NewError("socks failed to respond connect").Base(err))
					return
				}
				select {
				case s.connChan <- conn:
				case <-s.ctx.Done():
					log.Debug("exiting")
				}
			case Associate:
				log.Info("socks associate request from", conn.RemoteAddr(), "metadata", conn.Metadata())
				err = s.associate(conn, conn.Metadata().Address)
				if err != nil {
					log.Error(common.NewError("socks failed to respond associate").Base(err))
					return
				}
			default:
				log.Error("socks unknown command", conn.Metadata().Command)
			}
		}(conn)
	}
}

func (s *Server) Close() error {
	s.cancel()
	s.wg.Wait()
	s.listenPacketConn.Close()
	return s.underlay.Close()
}

func (s *Server) handshake(conn net.Conn) (*Conn, error) {
	version := [1]byte{}
	if _, err := conn.Read(version[:]); err != nil {
		return nil, common.NewError("failed to read socks version").Base(err)
	}
	if version[0] != 5 {
		return nil, common.NewError(fmt.Sprintf("invalid socks version %d", version[0]))
	}
	nmethods := [1]byte{}
	if _, err := conn.Read(nmethods[:]); err != nil {
		return nil, common.NewError("failed to read NMETHODS")
	}
	if _, err := io.CopyN(io.Discard, conn, int64(nmethods[0])); err != nil {
		return nil, common.NewError("socks failed to read methods").Base(err)
	}
	if _, err := conn.Write([]byte{0x5, 0x0}); err != nil {
		return nil, common.NewError("failed to respond auth").Base(err)
	}

	buf := [3]byte{}
	if _, err := conn.Read(buf[:]); err != nil {
		return nil, common.NewError("failed to read command")
	}

	addr := new(tunnel.Address)
	_, err := addr.ReadFrom(conn)
	if err != nil {
		return nil, err
	}

	return &Conn{
		metadata: &tunnel.Metadata{
			Command: tunnel.Command(buf[1]),
			Address: addr,
		},
		Conn: conn,
	}, nil
}

func (s *Server) connect(conn net.Conn) error {
	_, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	return err
}

func (s *Server) associate(conn net.Conn, addr *tunnel.Address) error {
	buf := bytes.NewBuffer([]byte{0x05, 0x00, 0x00})
	_, err := addr.WriteTo(buf)
	common.Must(err)
	_, err = conn.Write(buf.Bytes())
	return err
}

func (s *Server) packetDispatchLoop() {
	buf := make([]byte, MaxPacketSize)
	for {
		n, src, err := s.listenPacketConn.ReadFrom(buf)
		if err != nil {
			select {
			case <-s.ctx.Done():
				log.Debug("exiting")
				return
			default:
				continue
			}
		}
		log.Debug("socks recv udp packet from", src)
		s.mappingLock.RLock()
		conn, found := s.mapping[src.String()]
		s.mappingLock.RUnlock()
		if !found {
			ctx, cancel := context.WithCancel(s.ctx)
			conn = &PacketConn{
				input:      make(chan *packetInfo, 128),
				output:     make(chan *packetInfo, 128),
				ctx:        ctx,
				cancel:     cancel,
				PacketConn: s.listenPacketConn,
				src:        src,
			}
			s.wg.Add(1)
			go func(conn *PacketConn) {
				defer s.wg.Done()
				defer conn.Close()
				timeout := time.Second * 5
				timer := time.NewTimer(timeout)
				defer timer.Stop()
				responseBuf := make([]byte, MaxPacketSize)
				responseBuffer := bytes.NewBuffer(responseBuf[:0])
				for {
					select {
					case info := <-conn.output:
						responseBuffer.Reset()
						responseBuffer.Write([]byte{0, 0, 0}) // RSV, FRAG
						_, err := info.metadata.Address.WriteTo(responseBuffer)
						if err != nil {
							log.Error("socks failed to write address")
							return
						}
						responseBuffer.Write(info.payload)
						_, err = s.listenPacketConn.WriteTo(responseBuffer.Bytes(), conn.src)
						if err != nil {
							log.Error("socks failed to respond packet to", conn.src)
							return
						}
						log.Debug("socks respond udp packet to", conn.src, "metadata", info.metadata)
						if !timer.Stop() {
							<-timer.C
						}
						timer.Reset(timeout)
					case <-timer.C:
						log.Info("socks udp session timeout, closed")
						s.mappingLock.Lock()
						delete(s.mapping, conn.src.String())
						s.mappingLock.Unlock()
						return
					case <-conn.ctx.Done():
						s.mappingLock.Lock()
						delete(s.mapping, conn.src.String())
						s.mappingLock.Unlock()
						log.Info("socks udp session closed")
						return
					}
				}
			}(conn)

			s.mappingLock.Lock()
			s.mapping[src.String()] = conn
			s.mappingLock.Unlock()

			s.packetChan <- conn
			log.Info("socks new udp session from", src)
		}
		r := bytes.NewReader(buf[3:n])
		address := new(tunnel.Address)
		_, err = address.ReadFrom(r)
		if err != nil {
			log.Error(common.NewError("socks failed to parse incoming packet").Base(err))
			continue
		}
		remaining := r.Len()
		if remaining <= 0 {
			continue
		}
		payload := make([]byte, remaining)
		_, _ = r.Read(payload)
		select {
		case conn.input <- &packetInfo{
			metadata: &tunnel.Metadata{
				Address: address,
			},
			payload: payload,
		}:
		default:
		}
	}
}

func NewServer(ctx context.Context, underlay tunnel.Server) (*Server, error) {
	cfg := config.FromContext(ctx, Name).(*Config)
	ctx, cancel := context.WithCancel(ctx)
	listenPacketConn, err := underlay.AcceptPacket(nil)
	if err != nil {
		cancel()
		return nil, common.NewError("socks failed to accept packet conn").Base(err)
	}
	server := &Server{
		underlay:         underlay,
		localHost:        cfg.LocalHost,
		localPort:        cfg.LocalPort,
		timeout:          time.Duration(cfg.UDPTimeout) * time.Second,
		connChan:         make(chan tunnel.Conn, 32),
		packetChan:       make(chan tunnel.PacketConn, 32),
		mapping:          make(map[string]*PacketConn),
		ctx:              ctx,
		cancel:           cancel,
		listenPacketConn: listenPacketConn,
	}
	server.wg.Go(func() {
		server.acceptConnLoop()
	})
	server.wg.Go(func() {
		server.packetDispatchLoop()
	})
	return server, nil
}
