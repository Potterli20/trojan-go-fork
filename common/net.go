package common

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/database64128/tfo-go/v2"
)

const (
	KiB = 1024
	MiB = KiB * 1024
	GiB = MiB * 1024
)

func HumanFriendlyTraffic(bytes uint64) string {
	if bytes <= KiB {
		return fmt.Sprintf("%d B", bytes)
	}
	if bytes <= MiB {
		return fmt.Sprintf("%.2f KiB", float32(bytes)/KiB)
	}
	if bytes <= GiB {
		return fmt.Sprintf("%.2f MiB", float32(bytes)/MiB)
	}
	return fmt.Sprintf("%.2f GiB", float32(bytes)/GiB)
}

func PickPort(network string, host string) int {
	switch network {
	case "tcp":
		for range 16 {
			l, err := net.Listen("tcp", host+":0")
			if err != nil {
				continue
			}
			defer l.Close()
			_, port, err := net.SplitHostPort(l.Addr().String())
			Must(err)
			p, err := strconv.ParseInt(port, 10, 32)
			Must(err)
			return int(p)
		}
	case "udp":
		for range 16 {
			conn, err := net.ListenPacket("udp", host+":0")
			if err != nil {
				continue
			}
			defer conn.Close()
			_, port, err := net.SplitHostPort(conn.LocalAddr().String())
			Must(err)
			p, err := strconv.ParseInt(port, 10, 32)
			Must(err)
			return int(p)
		}
	default:
		return 0
	}
	return 0
}

func WriteAllBytes(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		n, err := writer.Write(payload)
		if err != nil {
			return err
		}
		payload = payload[n:]
	}
	return nil
}

func WriteFile(path string, payload []byte) error {
	writer, err := os.Create(path)
	if err != nil {
		return err
	}
	defer writer.Close()

	return WriteAllBytes(writer, payload)
}

func FetchHTTPContent(target string) ([]byte, error) {
	parsedTarget, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %s", target)
	}

	if s := strings.ToLower(parsedTarget.Scheme); s != "http" && s != "https" {
		return nil, fmt.Errorf("invalid scheme: %s", parsedTarget.Scheme)
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	resp, err := client.Do(&http.Request{
		Method: "GET",
		URL:    parsedTarget,
		Close:  true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to dial to %s", target)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status code: %d", resp.StatusCode)
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read HTTP response")
	}

	return content, nil
}

type DialConfig struct {
	Network       string
	Address       string
	EnableTFO     bool
	Timeout       time.Duration
	KeepAlive     bool
	NoDelay       bool
	PreferIPv4    bool
	RetryCount    int
	RetryInterval time.Duration
}

func Dial(ctx context.Context, cfg DialConfig) (net.Conn, error) {
	network := cfg.Network
	if cfg.PreferIPv4 && network == "tcp" {
		network = "tcp4"
	}

	var conn net.Conn
	var err error

	for attempt := 0; attempt <= cfg.RetryCount; attempt++ {
		if attempt > 0 {
			time.Sleep(cfg.RetryInterval)
		}

		if cfg.EnableTFO {
			dialer := &tfo.Dialer{
				Dialer: net.Dialer{
					Timeout: cfg.Timeout,
				},
				Fallback: true,
			}
			conn, err = dialer.DialContext(ctx, network, cfg.Address, nil)
			if err == nil {
				break
			}
		}

		dialer := &net.Dialer{
			Timeout: cfg.Timeout,
		}
		conn, err = dialer.DialContext(ctx, network, cfg.Address)
		if err == nil {
			break
		}
	}

	if err != nil {
		return nil, fmt.Errorf("dial failed after %d attempts: %w", cfg.RetryCount+1, err)
	}

	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(cfg.KeepAlive)
		tcpConn.SetNoDelay(cfg.NoDelay)
	}

	return conn, nil
}

type ListenConfig struct {
	EnableTFO bool
}

func Listen(ctx context.Context, cfg ListenConfig, network, address string) (net.Listener, error) {
	if cfg.EnableTFO {
		listener, err := tfo.ListenContext(ctx, network, address)
		if err == nil {
			return listener, nil
		}
	}

	return net.Listen(network, address)
}
