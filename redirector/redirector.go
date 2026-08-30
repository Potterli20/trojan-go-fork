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
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/log"
)

type Dial func(net.Addr) (net.Conn, error)

func defaultDial(addr net.Addr) (net.Conn, error) {
	// 带超时的拨号：Redirector.Close 的 wg.Wait 依赖 worker 退出，
	// 无超时拨号会被不可达目标拖住约 2 分钟
	d := net.Dialer{Timeout: 30 * time.Second}
	return d.Dial("tcp", addr.String())
}

type Redirection struct {
	Dial
	RedirectTo  net.Addr
	InboundConn net.Conn
	ClientIP    string
}

type Redirector struct {
	ctx             context.Context
	cancel          context.CancelFunc
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
			if n > 0 {
				headerBuf.Write(buf[:n])
			}
			if headerBuf.Len() > 0 {
				outbound.Write(headerBuf.Bytes())
			}
			return err
		}
		headerBuf.Write(buf[:n])

		if bytes.Contains(headerBuf.Bytes(), []byte("\r\n\r\n")) {
			break
		}

		if headerBuf.Len() > 65536 {
			outbound.Write(headerBuf.Bytes())
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

				var copyWg sync.WaitGroup

				copyWg.Go(func() {
					if _, err := io.Copy(outboundConn, redirection.InboundConn); err != nil {
						log.Debug(err)
					}
				})

				copyWg.Go(func() {
					if _, err := io.Copy(redirection.InboundConn, outboundConn); err != nil {
						log.Debug(err)
					}
				})

				// 不能用 "select ctx.Done / default" 判断关闭：ctx 恰已取消时两分支随机命中，
				// 可能误走 default 继续阻塞在 copyWg.Wait() 上；直接检查 ctx.Err()
				if r.ctx.Err() != nil {
					log.Debug("redirector shutting down")
					return
				}
				copyWg.Wait()
				log.Info("redirection done")
			})
		case <-r.ctx.Done():
			return
		}
	}
}

func NewRedirector(ctx context.Context) *Redirector {
	ctx, cancel := context.WithCancel(ctx)
	r := &Redirector{
		ctx:             ctx,
		cancel:          cancel,
		redirectionChan: make(chan *Redirection, 64),
	}
	r.wg.Go(func() {
		r.worker()
	})
	return r
}

func (r *Redirector) Close() error {
	r.cancel()
	r.wg.Wait()
	return nil
}
