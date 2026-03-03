package socks5

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"strings"
	"time"

	"github.com/Potterli20/golem/pkg/logger"
	"github.com/Potterli20/golem/pkg/metrics"
	"github.com/Potterli20/golem/pkg/zcache"
	"github.com/txthinking/runnergroup"
)

var (
	// ErrUnsupportCmd is the error when got unsupport command
	ErrUnsupportCmd = errors.New("Unsupport Command")
	// ErrUserPassAuth is the error when got invalid username or password
	ErrUserPassAuth = errors.New("Invalid Username or Password for Auth")
)

// Server is socks5 server wrapper
type Server struct {
	UserName          string
	Password          string
	Method            byte
	SupportedCommands []byte
	Addr              string
	ServerAddr        net.Addr
	UDPConn           *net.UDPConn
	UDPExchanges      zcache.LocalCache
	TCPTimeout        int
	UDPTimeout        int
	Handle            Handler
	AssociatedUDP     zcache.LocalCache
	UDPSrc            zcache.LocalCache
	RunnerGroup       *runnergroup.RunnerGroup
	// RFC: [UDP ASSOCIATE] The server MAY use this information to limit access to the association. Default false, no limit.
	LimitUDP bool
}

// UDPExchange used to store client address and remote connection
type UDPExchange struct {
	ClientAddr *net.UDPAddr
	RemoteConn net.Conn
}

// NewClassicServer return a server which allow none method
func NewClassicServer(addr, ip, username, password string, tcpTimeout, udpTimeout int) (*Server, error) {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	saddr, err := Resolve("udp", net.JoinHostPort(ip, p))
	if err != nil {
		return nil, err
	}
	m := MethodNone
	if username != "" && password != "" {
		m = MethodUsernamePassword
	}

	// 创建一个空的 metrics server
	ms := metrics.NewTaskMetrics("", "", "socks5")

	// 创建本地缓存配置
	localConfig := &zcache.LocalConfig{
		NumCounters:  1e7, // 跟踪的键数量
		MaxCostMB:    512, // 最大内存使用量
		BufferItems:  64,  // Get 缓冲区大小
		MetricServer: ms,  // 必需的 metrics server
		Logger:       logger.NewLogger(),
	}

	// 初始化三个本地缓存实例
	cs, err := zcache.NewLocalCache(localConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create UDPExchanges cache: %v", err)
	}

	cs1, err := zcache.NewLocalCache(localConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create AssociatedUDP cache: %v", err)
	}

	cs2, err := zcache.NewLocalCache(localConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create UDPSrc cache: %v", err)
	}

	s := &Server{
		Method:            m,
		UserName:          username,
		Password:          password,
		SupportedCommands: []byte{CmdConnect, CmdUDP},
		Addr:              addr,
		ServerAddr:        saddr,
		UDPExchanges:      cs,
		TCPTimeout:        tcpTimeout,
		UDPTimeout:        udpTimeout,
		AssociatedUDP:     cs1,
		UDPSrc:            cs2,
		RunnerGroup:       runnergroup.New(),
	}
	return s, nil
}

// Negotiate handle negotiate packet.
// This method do not handle gssapi(0x01) method now.
// Error or OK both replied.
func (s *Server) Negotiate(rw io.ReadWriter) error {
	rq, err := NewNegotiationRequestFrom(rw)
	if err != nil {
		return err
	}
	var got bool
	var m byte
	for _, m = range rq.Methods {
		if m == s.Method {
			got = true
		}
	}
	if !got {
		rp := NewNegotiationReply(MethodUnsupportAll)
		if _, err := rp.WriteTo(rw); err != nil {
			return err
		}
	}
	rp := NewNegotiationReply(s.Method)
	if _, err := rp.WriteTo(rw); err != nil {
		return err
	}

	if s.Method == MethodUsernamePassword {
		urq, err := NewUserPassNegotiationRequestFrom(rw)
		if err != nil {
			return err
		}
		if string(urq.Uname) != s.UserName || string(urq.Passwd) != s.Password {
			urp := NewUserPassNegotiationReply(UserPassStatusFailure)
			if _, err := urp.WriteTo(rw); err != nil {
				return err
			}
			return ErrUserPassAuth
		}
		urp := NewUserPassNegotiationReply(UserPassStatusSuccess)
		if _, err := urp.WriteTo(rw); err != nil {
			return err
		}
	}
	return nil
}

// GetRequest get request packet from client, and check command according to SupportedCommands
// Error replied.
func (s *Server) GetRequest(rw io.ReadWriter) (*Request, error) {
	r, err := NewRequestFrom(rw)
	if err != nil {
		return nil, err
	}
	var supported bool
	for _, c := range s.SupportedCommands {
		if r.Cmd == c {
			supported = true
			break
		}
	}
	if !supported {
		var p *Reply
		if r.Atyp == ATYPIPv4 || r.Atyp == ATYPDomain {
			p = NewReply(RepCommandNotSupported, ATYPIPv4, []byte{0x00, 0x00, 0x00, 0x00}, []byte{0x00, 0x00})
		} else {
			p = NewReply(RepCommandNotSupported, ATYPIPv6, []byte(net.IPv6zero), []byte{0x00, 0x00})
		}
		if _, err := p.WriteTo(rw); err != nil {
			return nil, err
		}
		return nil, ErrUnsupportCmd
	}
	return r, nil
}

// Run server
func (s *Server) ListenAndServe(h Handler) error {
	if h == nil {
		s.Handle = &DefaultHandle{}
	} else {
		s.Handle = h
	}
	addr, err := net.ResolveTCPAddr("tcp", s.Addr)
	if err != nil {
		return err
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return err
	}
	s.RunnerGroup.Add(&runnergroup.Runner{
		Start: func() error {
			for {
				c, err := l.AcceptTCP()
				if err != nil {
					return err
				}
				go func(c *net.TCPConn) {
					defer c.Close()
					if err := s.Negotiate(c); err != nil {
						log.Println(err)
						return
					}
					r, err := s.GetRequest(c)
					if err != nil {
						log.Println(err)
						return
					}
					if err := s.Handle.TCPHandle(s, c, r); err != nil {
						log.Println(err)
					}
				}(c)
			}
			return nil
		},
		Stop: func() error {
			return l.Close()
		},
	})
	addr1, err := net.ResolveUDPAddr("udp", s.Addr)
	if err != nil {
		l.Close()
		return err
	}
	s.UDPConn, err = net.ListenUDP("udp", addr1)
	if err != nil {
		l.Close()
		return err
	}
	s.RunnerGroup.Add(&runnergroup.Runner{
		Start: func() error {
			for {
				b := make([]byte, 65507)
				n, addr, err := s.UDPConn.ReadFromUDP(b)
				if err != nil {
					return err
				}
				go func(addr *net.UDPAddr, b []byte) {
					d, err := NewDatagramFromBytes(b)
					if err != nil {
						log.Println(err)
						return
					}
					if d.Frag != 0x00 {
						log.Println("Ignore frag", d.Frag)
						return
					}
					if err := s.Handle.UDPHandle(s, addr, d); err != nil {
						log.Println(err)
						return
					}
				}(addr, b[0:n])
			}
			return nil
		},
		Stop: func() error {
			return s.UDPConn.Close()
		},
	})
	return s.RunnerGroup.Wait()
}

// Stop server
func (s *Server) Shutdown() error {
	return s.RunnerGroup.Done()
}

// Handler handle tcp, udp request
type Handler interface {
	// Request has not been replied yet
	TCPHandle(*Server, *net.TCPConn, *Request) error
	UDPHandle(*Server, *net.UDPAddr, *Datagram) error
}

// DefaultHandle implements Handler interface
type DefaultHandle struct {
}

// TCPHandle auto handle request. You may prefer to do yourself.
func (h *DefaultHandle) TCPHandle(s *Server, c *net.TCPConn, r *Request) error {
	if r.Cmd == CmdConnect {
		rc, err := r.Connect(c)
		if err != nil {
			return err
		}
		defer rc.Close()
		go func() {
			var bf [1024 * 2]byte
			for {
				if s.TCPTimeout != 0 {
					if err := rc.SetDeadline(time.Now().Add(time.Duration(s.TCPTimeout) * time.Second)); err != nil {
						return
					}
				}
				i, err := rc.Read(bf[:])
				if err != nil {
					return
				}
				if _, err := c.Write(bf[0:i]); err != nil {
					return
				}
			}
		}()
		var bf [1024 * 2]byte
		for {
			if s.TCPTimeout != 0 {
				if err := c.SetDeadline(time.Now().Add(time.Duration(s.TCPTimeout) * time.Second)); err != nil {
					return nil
				}
			}
			i, err := c.Read(bf[:])
			if err != nil {
				return nil
			}
			if _, err := rc.Write(bf[0:i]); err != nil {
				return nil
			}
		}
		return nil
	}
	if r.Cmd == CmdUDP {
		caddr, err := r.UDP(c, s.ServerAddr)
		if err != nil {
			return err
		}
		ch := make(chan byte)
		defer close(ch)
		err = s.AssociatedUDP.Set(context.Background(), caddr.String(), ch, 24*time.Hour)
		if err != nil {
			return fmt.Errorf("failed to set AssociatedUDP cache: %v", err)
		}
		defer s.AssociatedUDP.Delete(context.Background(), caddr.String())
		io.Copy(ioutil.Discard, c)
		if Debug {
			log.Printf("A tcp connection that udp %#v associated closed\n", caddr.String())
		}
		return nil
	}
	return ErrUnsupportCmd
}

// UDPHandle auto handle packet. You may prefer to do yourself.
func (h *DefaultHandle) UDPHandle(s *Server, addr *net.UDPAddr, d *Datagram) error {
	src := addr.String()
	var ch chan byte
	if s.LimitUDP {
		var chInterface any
		err := s.AssociatedUDP.Get(context.Background(), src, &chInterface)
		if err != nil {
			if s.AssociatedUDP.IsNotFoundError(err) {
				return fmt.Errorf("This udp address %s is not associated with tcp", src)
			}
			return fmt.Errorf("failed to get from AssociatedUDP cache: %v", err)
		}
		ch = chInterface.(chan byte)
	}
	send := func(ue *UDPExchange, data []byte) error {
		select {
		case <-ch:
			return fmt.Errorf("This udp address %s is not associated with tcp", src)
		default:
			_, err := ue.RemoteConn.Write(data)
			if err != nil {
				return err
			}
			if Debug {
				log.Printf("Sent UDP data to remote. client: %#v server: %#v remote: %#v data: %#v\n", ue.ClientAddr.String(), ue.RemoteConn.LocalAddr().String(), ue.RemoteConn.RemoteAddr().String(), data)
			}
		}
		return nil
	}

	dst := d.Address()
	var ue *UDPExchange
	var iue any
	err := s.UDPExchanges.Get(context.Background(), src+dst, &iue)
	if err == nil {
		ue = iue.(*UDPExchange)
		return send(ue, d.Data)
	} else if !s.UDPExchanges.IsNotFoundError(err) {
		return fmt.Errorf("failed to get from UDPExchanges cache: %v", err)
	}

	if Debug {
		log.Printf("Call udp: %#v\n", dst)
	}
	var laddr string
	var laddrInterface any
	err = s.UDPSrc.Get(context.Background(), src+dst, &laddrInterface)
	if err == nil {
		laddr = laddrInterface.(string)
	} else if !s.UDPSrc.IsNotFoundError(err) {
		return fmt.Errorf("failed to get from UDPSrc cache: %v", err)
	}
	rc, err := DialUDP("udp", laddr, dst)
	if err != nil {
		if !strings.Contains(err.Error(), "address already in use") && !strings.Contains(err.Error(), "can't assign requested address") {
			return err
		}
		rc, err = DialUDP("udp", "", dst)
		if err != nil {
			return err
		}
		laddr = ""
	}
	if laddr == "" {
		err = s.UDPSrc.Set(context.Background(), src+dst, rc.LocalAddr().String(), 24*time.Hour)
		if err != nil {
			return fmt.Errorf("failed to set UDPSrc cache: %v", err)
		}
	}
	ue = &UDPExchange{
		ClientAddr: addr,
		RemoteConn: rc,
	}
	if Debug {
		log.Printf("Created remote UDP conn for client. client: %#v server: %#v remote: %#v\n", addr.String(), ue.RemoteConn.LocalAddr().String(), d.Address())
	}
	if err := send(ue, d.Data); err != nil {
		ue.RemoteConn.Close()
		return err
	}
	err = s.UDPExchanges.Set(context.Background(), src+dst, ue, 24*time.Hour)
	if err != nil {
		return fmt.Errorf("failed to set UDPExchanges cache: %v", err)
	}
	go func(ue *UDPExchange, dst string) {
		defer func() {
			ue.RemoteConn.Close()
			if err := s.UDPExchanges.Delete(context.Background(), ue.ClientAddr.String()+dst); err != nil {
				log.Printf("failed to delete from UDPExchanges cache: %v", err)
			}
		}()
		var b [65507]byte
		for {
			select {
			case <-ch:
				if Debug {
					log.Printf("The tcp that udp address %s associated closed\n", ue.ClientAddr.String())
				}
				return
			default:
				if s.UDPTimeout != 0 {
					if err := ue.RemoteConn.SetDeadline(time.Now().Add(time.Duration(s.UDPTimeout) * time.Second)); err != nil {
						log.Println(err)
						return
					}
				}
				n, err := ue.RemoteConn.Read(b[:])
				if err != nil {
					return
				}
				if Debug {
					log.Printf("Got UDP data from remote. client: %#v server: %#v remote: %#v data: %#v\n", ue.ClientAddr.String(), ue.RemoteConn.LocalAddr().String(), ue.RemoteConn.RemoteAddr().String(), b[0:n])
				}
				a, addr, port, err := ParseAddress(dst)
				if err != nil {
					log.Println(err)
					return
				}
				if a == ATYPDomain {
					addr = addr[1:]
				}
				d1 := NewDatagram(a, addr, port, b[0:n])
				if _, err := s.UDPConn.WriteToUDP(d1.Bytes(), ue.ClientAddr); err != nil {
					return
				}
				if Debug {
					log.Printf("Sent Datagram. client: %#v server: %#v remote: %#v data: %#v %#v %#v %#v %#v %#v datagram address: %#v\n", ue.ClientAddr.String(), ue.RemoteConn.LocalAddr().String(), ue.RemoteConn.RemoteAddr().String(), d1.Rsv, d1.Frag, d1.Atyp, d1.DstAddr, d1.DstPort, d1.Data, d1.Address())
				}
			}
		}
	}(ue, dst)
	return nil
}
