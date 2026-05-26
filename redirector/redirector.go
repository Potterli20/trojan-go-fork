package redirector

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/log"
)

type Dial func(net.Addr) (net.Conn, error)

func defaultDial(addr net.Addr) (net.Conn, error) {
	return net.Dial("tcp", addr.String())
}

type Redirection struct {
	Dial
	RedirectTo  net.Addr
	InboundConn net.Conn
	ClientIP    string
}

type Redirector struct {
	ctx             context.Context
	wg              sync.WaitGroup
	redirectionChan chan *Redirection
}

func (r *Redirector) Redirect(redirection *Redirection) {
	select {
	case r.redirectionChan <- redirection:
		log.Debug("redirect request ")
	case <-r.ctx.Done():
		log.Debug("exiting")
	}
}

func injectForwardedHeader(inbound net.Conn, outbound net.Conn, clientIP string) error {
	var headerBuf bytes.Buffer
	buf := make([]byte, 4096)

	for {
		n, err := inbound.Read(buf)
		if err != nil {
			return err
		}
		headerBuf.Write(buf[:n])

		if bytes.Contains(headerBuf.Bytes(), []byte("\r\n\r\n")) {
			break
		}

		if headerBuf.Len() > 65536 {
			return fmt.Errorf("headers too large")
		}
	}

	headerBytes := headerBuf.Bytes()
	before, after, _ := bytes.Cut(headerBytes, []byte("\r\n\r\n"))

	headers := before
	remaining := after

	headerStr := string(headers)
	lines := strings.Split(headerStr, "\r\n")

	xffFound := false
	for i, line := range lines {
		if strings.HasPrefix(strings.ToLower(line), "x-forwarded-for:") {
			lines[i] = line + ", " + clientIP
			xffFound = true
			break
		}
	}
	if !xffFound {
		lines = append(lines, "X-Forwarded-For: "+clientIP)
	}

	lines = append(lines, "X-Real-IP: "+clientIP)

	var out bytes.Buffer
	for _, line := range lines {
		out.WriteString(line)
		out.WriteString("\r\n")
	}
	out.WriteString("\r\n")
	out.Write(remaining)

	_, err := outbound.Write(out.Bytes())
	return err
}

func (r *Redirector) worker() {
	for {
		select {
		case redirection, ok := <-r.redirectionChan:
			if !ok {
				return
			}
			r.wg.Go(func() {
				if redirection.InboundConn == nil || reflect.ValueOf(redirection.InboundConn).IsNil() {
					log.Error("nil inbound conn")
					return
				}
				defer redirection.InboundConn.Close()
				if redirection.RedirectTo == nil || reflect.ValueOf(redirection.RedirectTo).IsNil() {
					log.Error("nil redirection addr")
					return
				}
				if redirection.Dial == nil {
					redirection.Dial = defaultDial
				}
				log.Warn("redirecting connection from", redirection.InboundConn.RemoteAddr(), "to", redirection.RedirectTo.String())
				outboundConn, err := redirection.Dial(redirection.RedirectTo)
				if err != nil {
					log.Error(common.NewError("failed to redirect to target address").Base(err))
					return
				}
				defer outboundConn.Close()
				if redirection.ClientIP != "" {
					if err := injectForwardedHeader(redirection.InboundConn, outboundConn, redirection.ClientIP); err != nil {
						log.Debug("failed to inject X-Forwarded-For header, using plain TCP forwarding:", err)
					}
				}

				done := make(chan struct{})
				var once sync.Once
				closeDone := func() { once.Do(func() { close(done) }) }

				go func() {
					_, err := io.Copy(outboundConn, redirection.InboundConn)
					if err != nil {
						log.Debug(err)
					}
					closeDone()
				}()
				go func() {
					_, err := io.Copy(redirection.InboundConn, outboundConn)
					if err != nil {
						log.Debug(err)
					}
					closeDone()
				}()

				select {
				case <-done:
					log.Info("redirection done")
				case <-r.ctx.Done():
					log.Debug("redirector shutting down")
				}
			})
		case <-r.ctx.Done():
			return
		}
	}
}

func NewRedirector(ctx context.Context) *Redirector {
	r := &Redirector{
		ctx:             ctx,
		redirectionChan: make(chan *Redirection, 64),
	}
	go r.worker()
	return r
}

func (r *Redirector) Close() error {
	close(r.redirectionChan)
	r.wg.Wait()
	return nil
}
