package tls

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/redirector"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/Potterli20/trojan-go-fork/tunnel/tls/fingerprint"
	"github.com/Potterli20/trojan-go-fork/tunnel/transport"
	"github.com/Potterli20/trojan-go-fork/tunnel/websocket"
)

// firstByteTimeout 限定 TLS 握手与 HTTP 嗅探阶段等待对端数据的时限
const firstByteTimeout = 30 * time.Second

// Server is a tls server
type Server struct {
	fallbackAddress *tunnel.Address
	verifySNI       bool
	sni             string
	alpn            []string
	keyPair         []tls.Certificate
	keyPairLock     sync.RWMutex
	httpResp        []byte
	cipherSuite     []uint16
	sessionTicket   bool
	keyLogger       io.WriteCloser
	connChan        chan tunnel.Conn
	wsChan          chan tunnel.Conn
	redir           *redirector.Redirector
	ctx             context.Context
	cancel          context.CancelFunc
	underlay        tunnel.Server
	nextHTTP        atomic.Int32
	wg              sync.WaitGroup
}

func (s *Server) Close() error {
	s.cancel()
	// 先关闭底层 transport 解除 acceptLoop 的 AcceptConn 阻塞，否则 wg.Wait() 会永久死锁
	err := s.underlay.Close()
	s.wg.Wait()
	if s.keyLogger != nil {
		s.keyLogger.Close()
	}
	return err
}

func isDomainNameMatched(pattern string, domainName string) bool {
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[2:]
		domainPrefixLen := len(domainName) - len(suffix) - 1
		return strings.HasSuffix(domainName, suffix) && domainPrefixLen > 0 && !strings.Contains(domainName[:domainPrefixLen], ".")
	}
	return pattern == domainName
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.underlay.AcceptConn(&Tunnel{})
		if err != nil {
			// 注意：这里不能用 "select ctx.Done / default" 模式判断是否在关闭：
			// default 永远就绪，当 ctx 恰已取消时两个分支同时就绪，runtime 随机二选一，
			// 会以约 50% 概率在正常关闭路径上触发 Fatal（os.Exit）杀死整个进程。
			if s.ctx.Err() != nil {
				return
			}
			// 底层已不可用，accept 循环终止；用 Error 而非 Fatal，进程不应被库代码杀死
			log.Error(common.NewError("transport accept error" + err.Error()))
			return
		}
		s.wg.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					log.Error(common.NewError("panic in tls handler: " + fmt.Sprintf("%v", r)))
					conn.Close()
				}
			}()

			tlsConfig := &tls.Config{
				CipherSuites:           s.cipherSuite,
				SessionTicketsDisabled: !s.sessionTicket,
				NextProtos:             s.alpn,
				KeyLogWriter:           s.keyLogger,
				GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
					s.keyPairLock.RLock()
					defer s.keyPairLock.RUnlock()
					if len(s.keyPair) == 0 {
						return nil, common.NewError("no certificate available")
					}
					sni := s.keyPair[0].Leaf.Subject.CommonName
					dnsNames := s.keyPair[0].Leaf.DNSNames
					if s.sni != "" {
						sni = s.sni
					}
					if s.verifySNI {
						matched := isDomainNameMatched(sni, hello.ServerName)
						for _, name := range dnsNames {
							if isDomainNameMatched(name, hello.ServerName) {
								matched = true
								break
							}
						}
						if !matched {
							expected := sni + " or " + strings.Join(dnsNames, "/")
							return nil, common.NewError("sni mismatched: " + hello.ServerName + ", expected: " + expected)
						}
					}
					return &s.keyPair[0], nil
				},
			}

			handshakeRewindConn := common.NewRewindConn(conn)
			handshakeRewindConn.SetBufferSize(2048)
			defer handshakeRewindConn.StopBuffering()

			// 握手与后续 HTTP 嗅探期间对端可能静默不发数据；不设截止时间会让
			// handler goroutine 永久阻塞，Close() 的 wg.Wait() 随之挂起
			conn.SetDeadline(time.Now().Add(firstByteTimeout))
			tlsConn := tls.Server(handshakeRewindConn, tlsConfig)
			err = tlsConn.Handshake()

			if err != nil {
				if strings.Contains(err.Error(), "first record does not look like a TLS handshake") {
					handshakeRewindConn.Rewind()
					log.Error(common.NewError("failed to perform tls handshake with " + conn.RemoteAddr().String() + ", redirecting").Base(err))
					switch {
					case s.fallbackAddress != nil:
						s.redir.Redirect(&redirector.Redirection{
							InboundConn: handshakeRewindConn,
							RedirectTo:  s.fallbackAddress,
						})
					case s.httpResp != nil:
						handshakeRewindConn.Write(s.httpResp)
						handshakeRewindConn.Close()
					default:
						handshakeRewindConn.Close()
					}
				} else {
					tlsConn.Close()
					log.Error(common.NewError("tls handshake failed").Base(err))
				}
				return
			}

			log.Debug("tls connection from", conn.RemoteAddr())
			state := tlsConn.ConnectionState()
			log.Trace("tls handshake", tls.CipherSuiteName(state.CipherSuite), state.DidResume, state.NegotiatedProtocol)

			// we use a real http header parser to mimic a real http server
			rewindConn := common.NewRewindConn(tlsConn)
			rewindConn.SetBufferSize(1024)
			r := bufio.NewReader(rewindConn)
			httpReq, err := http.ReadRequest(r)
			rewindConn.Rewind()
			rewindConn.StopBuffering()
			// 连接即将移交下游长期使用，必须解除上面的截止时间
			conn.SetDeadline(time.Time{})
			if err != nil {
				// this is not a http request. pass it to trojan protocol layer for further inspection
				select {
				case s.connChan <- &transport.Conn{
					Conn: rewindConn,
				}:
				case <-s.ctx.Done():
					// 下游（trojan server）已停止消费，必须关闭连接并退出，
					// 否则该 goroutine 永久阻塞在 channel 发送上，Close() 的 wg.Wait() 随之死锁。
					rewindConn.Close()
				}
			} else {
				if s.nextHTTP.Load() != 1 {
					// there is no websocket layer waiting for connections, redirect it
					log.Error("incoming http request, but no websocket server is listening")
					s.redir.Redirect(&redirector.Redirection{
						InboundConn: rewindConn,
						RedirectTo:  s.fallbackAddress,
					})
					return
				}
				// this is a http request, pass it to websocket protocol layer
				log.Debug("http req: ", httpReq)
				select {
				case s.wsChan <- &transport.Conn{
					Conn: rewindConn,
				}:
				case <-s.ctx.Done():
					rewindConn.Close()
				}
			}
		})
	}
}

func (s *Server) AcceptConn(overlay tunnel.Tunnel) (tunnel.Conn, error) {
	if _, ok := overlay.(*websocket.Tunnel); ok {
		s.nextHTTP.Store(1)
		log.Debug("next proto http")
		// websocket overlay
		select {
		case conn := <-s.wsChan:
			return conn, nil
		case <-s.ctx.Done():
			return nil, common.NewError("transport server closed")
		}
	}
	// trojan overlay
	select {
	case conn := <-s.connChan:
		return conn, nil
	case <-s.ctx.Done():
		return nil, common.NewError("transport server closed")
	}
}

func (s *Server) AcceptPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	panic("not supported")
}

func (s *Server) checkKeyPairLoop(checkRate time.Duration, keyPath string, certPath string, password string) {
	var lastKeyBytes, lastCertBytes []byte
	ticker := time.NewTicker(checkRate)
	defer ticker.Stop()

	for {
		log.Debug("checking cert...")
		keyBytes, err := os.ReadFile(keyPath)
		if err != nil {
			log.Error(common.NewError("tls failed to check key").Base(err))
			if !s.waitForNextTick(ticker) {
				return
			}
			continue
		}
		certBytes, err := os.ReadFile(certPath)
		if err != nil {
			log.Error(common.NewError("tls failed to check cert").Base(err))
			if !s.waitForNextTick(ticker) {
				return
			}
			continue
		}
		if !bytes.Equal(keyBytes, lastKeyBytes) || !bytes.Equal(lastCertBytes, certBytes) {
			log.Info("new key pair detected")
			keyPair, err := loadKeyPair(keyPath, certPath, password)
			if err != nil {
				log.Error(common.NewError("tls failed to load new key pair").Base(err))
				if !s.waitForNextTick(ticker) {
					return
				}
				continue
			}
			s.keyPairLock.Lock()
			s.keyPair = []tls.Certificate{*keyPair}
			lastKeyBytes = keyBytes
			lastCertBytes = certBytes
			s.keyPairLock.Unlock()
		}
		if !s.waitForNextTick(ticker) {
			return
		}
	}
}

func (s *Server) waitForNextTick(ticker *time.Ticker) bool {
	select {
	case <-ticker.C:
		return true
	case <-s.ctx.Done():
		return false
	}
}

func loadKeyPair(keyPath string, certPath string, password string) (*tls.Certificate, error) {
	if password != "" {
		keyFile, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, common.NewError("failed to load key file").Base(err)
		}
		keyBlock, _ := pem.Decode(keyFile)
		if keyBlock == nil {
			return nil, common.NewError("failed to decode key file").Base(err)
		}
		// DecryptPEMBlock 自 Go 1.16 起标记废弃（RFC 1423 本身不安全），但标准库无替代实现；
		// 保留以兼容配置了 key_password 的传统 PEM 加密私钥，解密结果只用于本地 TLS 密钥加载
		decryptedKey, err := x509.DecryptPEMBlock(keyBlock, []byte(password))
		if err != nil {
			return nil, common.NewError("failed to decrypt key").Base(err)
		}

		certFile, err := os.ReadFile(certPath)
		certBlock, _ := pem.Decode(certFile)
		if certBlock == nil {
			return nil, common.NewError("failed to decode cert file").Base(err)
		}

		keyPair, err := tls.X509KeyPair(certBlock.Bytes, decryptedKey)
		if err != nil {
			return nil, err
		}
		keyPair.Leaf, err = x509.ParseCertificate(keyPair.Certificate[0])
		if err != nil {
			return nil, common.NewError("failed to parse leaf certificate").Base(err)
		}

		return &keyPair, nil
	}
	keyPair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, common.NewError("failed to load key pair").Base(err)
	}
	keyPair.Leaf, err = x509.ParseCertificate(keyPair.Certificate[0])
	if err != nil {
		return nil, common.NewError("failed to parse leaf certificate").Base(err)
	}
	return &keyPair, nil
}

// NewServer creates a tls layer server
func NewServer(ctx context.Context, underlay tunnel.Server) (*Server, error) {
	cfg := config.FromContext(ctx, Name).(*Config)

	var fallbackAddress *tunnel.Address
	var httpResp []byte
	if cfg.TLS.FallbackPort != 0 {
		if cfg.TLS.FallbackHost == "" {
			cfg.TLS.FallbackHost = cfg.RemoteHost
			log.Warn("empty tls fallback address")
		}
		fallbackAddress = tunnel.NewAddressFromHostPort("tcp", cfg.TLS.FallbackHost, cfg.TLS.FallbackPort)
		fallbackConn, err := net.Dial("tcp", fallbackAddress.String())
		if err != nil {
			return nil, common.NewError("invalid fallback address").Base(err)
		}
		fallbackConn.Close()
	} else {
		log.Warn("empty tls fallback port")
		if cfg.TLS.HTTPResponseFileName != "" {
			httpRespBody, err := os.ReadFile(cfg.TLS.HTTPResponseFileName)
			if err != nil {
				return nil, common.NewError("invalid response file").Base(err)
			}
			httpResp = httpRespBody
		} else {
			log.Warn("empty tls http response")
		}
	}

	keyPair, err := loadKeyPair(cfg.TLS.KeyPath, cfg.TLS.CertPath, cfg.TLS.KeyPassword)
	if err != nil {
		return nil, common.NewError("tls failed to load key pair")
	}

	var keyLogger io.WriteCloser
	if cfg.TLS.KeyLogPath != "" {
		log.Warn("tls key logging activated. USE OF KEY LOGGING COMPROMISES SECURITY. IT SHOULD ONLY BE USED FOR DEBUGGING.")
		file, err := os.OpenFile(cfg.TLS.KeyLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, common.NewError("failed to open key log file").Base(err)
		}
		keyLogger = file
	}

	var cipherSuite []uint16
	if len(cfg.TLS.Cipher) != 0 {
		cipherSuite = fingerprint.ParseCipher(strings.Split(cfg.TLS.Cipher, ":"))
	}

	ctx, cancel := context.WithCancel(ctx)
	server := &Server{
		underlay:        underlay,
		fallbackAddress: fallbackAddress,
		httpResp:        httpResp,
		verifySNI:       cfg.TLS.VerifyHostName,
		sni:             cfg.TLS.SNI,
		alpn:            cfg.TLS.ALPN,
		sessionTicket:   cfg.TLS.ReuseSession,
		connChan:        make(chan tunnel.Conn, 32),
		wsChan:          make(chan tunnel.Conn, 32),
		redir:           redirector.NewRedirector(ctx),
		keyPair:         []tls.Certificate{*keyPair},
		keyLogger:       keyLogger,
		cipherSuite:     cipherSuite,
		ctx:             ctx,
		cancel:          cancel,
	}

	server.wg.Go(func() {
		server.acceptLoop()
	})
	if cfg.TLS.CertCheckRate > 0 {
		server.wg.Go(func() {
			server.checkKeyPairLoop(
				time.Second*time.Duration(cfg.TLS.CertCheckRate),
				cfg.TLS.KeyPath,
				cfg.TLS.CertPath,
				cfg.TLS.KeyPassword,
			)
		})
	}

	log.Debug("tls server created")
	return server, nil
}
