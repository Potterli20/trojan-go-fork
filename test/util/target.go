package util

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/log"
)

var (
	HTTPAddr string
	HTTPPort string
)

func runHelloHTTPServer() {
	httpHello := func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte("HelloWorld"))
	}

	wsHandler := func(w http.ResponseWriter, req *http.Request) {
		sanitizedURL := strings.ReplaceAll(req.URL.String(), "\n", "")
		sanitizedURL = strings.ReplaceAll(sanitizedURL, "\r", "")
		sanitizedOrigin := strings.ReplaceAll(req.Header.Get("Origin"), "\n", "")
		sanitizedOrigin = strings.ReplaceAll(sanitizedOrigin, "\r", "")
		log.Debug("websocket url", sanitizedURL, "origin", sanitizedOrigin)
		conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		if err := conn.Write(context.Background(), websocket.MessageBinary, []byte("HelloWorld")); err != nil {
			return
		}
		// 等待对端关闭,handler 返回即断开连接
		for {
			if _, _, err := conn.Reader(context.Background()); err != nil {
				return
			}
		}
	}

	mux := &http.ServeMux{}
	mux.HandleFunc("/", httpHello)
	mux.HandleFunc("/websocket", wsHandler)
	HTTPAddr = GetTestAddr()
	_, HTTPPort, _ = net.SplitHostPort(HTTPAddr)
	server := http.Server{
		Addr:    HTTPAddr,
		Handler: mux,
	}
	go server.ListenAndServe()
	time.Sleep(time.Second * 1) // wait for http server
	fmt.Println("http test server listening on", HTTPAddr)
	wg.Done()
}

var (
	EchoAddr string
	EchoPort int
)

func runTCPEchoServer() {
	listener, err := net.Listen("tcp", EchoAddr)
	common.Must(err)
	wg.Done()
	go func() {
		defer listener.Close()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				for {
					buf := make([]byte, 2048)
					conn.SetDeadline(time.Now().Add(time.Second * 5))
					n, err := conn.Read(buf)
					conn.SetDeadline(time.Time{})
					if err != nil {
						return
					}
					_, err = conn.Write(buf[0:n])
					if err != nil {
						return
					}
				}
			}(conn)
		}
	}()
}

func runUDPEchoServer() {
	conn, err := net.ListenPacket("udp", EchoAddr)
	common.Must(err)
	wg.Done()
	go func() {
		for {
			buf := make([]byte, 1024*8)
			n, addr, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			log.Info("Echo from", addr)
			conn.WriteTo(buf[0:n], addr)
		}
	}()
}

func GeneratePayload(length int) []byte {
	buf := make([]byte, length)
	io.ReadFull(rand.Reader, buf)
	return buf
}

var (
	BlackHoleAddr string
	BlackHolePort int
)

func runTCPBlackHoleServer() {
	listener, err := net.Listen("tcp", BlackHoleAddr)
	common.Must(err)
	wg.Done()
	go func() {
		defer listener.Close()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				io.Copy(io.Discard, conn)
				conn.Close()
			}(conn)
		}
	}()
}

func runUDPBlackHoleServer() {
	conn, err := net.ListenPacket("udp", BlackHoleAddr)
	common.Must(err)
	wg.Done()
	go func() {
		defer conn.Close()
		buf := make([]byte, 1024*8)
		for {
			_, _, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
		}
	}()
}

var wg = sync.WaitGroup{}

func init() {
	wg.Add(5)
	runHelloHTTPServer()

	EchoPort = common.PickPort("tcp", "127.0.0.1")
	EchoAddr = fmt.Sprintf("127.0.0.1:%d", EchoPort)

	BlackHolePort = common.PickPort("tcp", "127.0.0.1")
	BlackHoleAddr = fmt.Sprintf("127.0.0.1:%d", BlackHolePort)

	runTCPEchoServer()
	runUDPEchoServer()

	runTCPBlackHoleServer()
	runUDPBlackHoleServer()

	wg.Wait()
}
