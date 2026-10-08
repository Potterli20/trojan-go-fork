package redirector

import (
	"bytes"
	"context"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
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

// 以下限时都作用在 misdirection（诱饵）路径上：这条路径处理的是认证失败的流量，
// 发送方无需任何凭据就能占用资源，所以只能由本层自己兜住。
// 之前两个 io.Copy 没有截止时间，且 cancel() 不关闭连接，于是「发几个字节就静默」
// 的对端会让每条连接永久留下 3 个 goroutine 和 2 个 fd，并把 Close() 的 wg.Wait 挂死。
var (
	redirectionIdleTimeout = 60 * time.Second // 双向都无流量多久后判定对端已放弃
	redirectionMaxLife     = 10 * time.Minute // 单次重定向的最长生命：堵住慢速滴流
	redirectionTick        = 5 * time.Second  // 截止时间巡检间隔
	redirectionDrainWait   = 2 * time.Second  // 关停时等待中继退出的上限
)

const copyBufferSize = 8 * 1024

// copyBufPool 替代 io.Copy 内部的全局 32KB 缓冲：诱饵路径不需要 32KB，
// 而每个在途会话都会占住一整块，驻留量随并发会话数线性增长。
var copyBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, copyBufferSize)
		return &buf
	},
}

func getCopyBuffer() []byte { return *copyBufPool.Get().(*[]byte) }

func putCopyBuffer(buf []byte) {
	// 与 proxy.boundedBufPool 同理：尺寸不符就交给 GC，避免池内驻留被撑大
	if len(buf) != copyBufferSize {
		return
	}
	copyBufPool.Put(&buf)
}

// activityConn 把双向流量累加到同一个计数器上，keeper 据此决定要不要顺延截止时间：
// 只要还有一个方向在动，就不该掐断整条会话（诱饵站在发大文件时客户端不回字节）。
type activityConn struct {
	net.Conn
	activity *atomic.Uint64
}

func (c *activityConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.activity.Add(uint64(n))
	}
	return n, err
}

func (c *activityConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.activity.Add(uint64(n))
	}
	return n, err
}

// arm 重设双向（读+写）截止时间。写方向也要限时：对端停止收取时阻塞点在 Write，
// 只设读截止会让那个 goroutine 永远出不去。
func (c *activityConn) arm(deadline time.Time) {
	_ = c.Conn.SetDeadline(deadline) //gosec:disable -- 错误忽略：限时失败时仍由 redirectionMaxLife 兜底
}

// superviseCopies 等待两个方向的中继结束，并维持空闲截止时间。
// finished 每有一个方向退出就收到一个信号；ctx 取消时主动关闭两端唤醒阻塞中的
// Read/Write，否则 cancel 无法解除 io.Copy 的阻塞。
// 返回 false 表示关停时有方向未在 redirectionDrainWait 内确认退出。
func (r *Redirector) superviseCopies(finished chan struct{}, inbound, outbound *activityConn) bool {
	const directions = 2
	pending := directions
	ticker := time.NewTicker(redirectionTick)
	defer ticker.Stop()
	last := inbound.activity.Load()

	// 先武装一次：从始至终一个字节都不发的对端也得计时，
	// 只在「有流量」时顺延会让这种连接永远不超时
	now := time.Now()
	inbound.arm(now.Add(redirectionIdleTimeout))
	outbound.arm(now.Add(redirectionIdleTimeout))
	// 硬上限：到点直接关闭两端，杜绝「每 50 秒滴一个字节」的常驻会话
	killer := time.AfterFunc(redirectionMaxLife, func() {
		inbound.Conn.Close()
		outbound.Conn.Close()
	})
	defer killer.Stop()

	for pending > 0 {
		select {
		case <-finished:
			pending--
		case <-r.ctx.Done():
			inbound.Conn.Close()
			outbound.Conn.Close()
			// 关闭后各方向会报错退出；给足上限而不是无限等，Close() 才能返回
			for pending > 0 {
				deadline := time.NewTimer(redirectionDrainWait)
				select {
				case <-finished:
					pending--
					deadline.Stop()
				case <-deadline.C:
					log.Warn("redirection copies did not finish within ", redirectionDrainWait)
					pending = 0
				}
			}
			return false
		case <-ticker.C:
			if n := inbound.activity.Load(); n != last {
				last = n
				at := time.Now().Add(redirectionIdleTimeout)
				inbound.arm(at)
				outbound.arm(at)
			}
			// 无流量就不顺延，先前设置的截止时间到点让 io.Copy 报错退出
		}
	}
	return true
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
				outbound.Write(headerBuf.Bytes()) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			}
			return err
		}
		headerBuf.Write(buf[:n])

		if bytes.Contains(headerBuf.Bytes(), []byte("\r\n\r\n")) {
			break
		}

		if headerBuf.Len() > 65536 {
			outbound.Write(headerBuf.Bytes()) //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			return common.NewError("headers too large")
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
					// 注入阶段同样要对静默对端限时：这里阻塞在 inbound.Read 上，
					// 而上游交出连接前已把认证阶段的读截止时间清掉了
					at := time.Now().Add(redirectionIdleTimeout)
					_ = redirection.InboundConn.SetReadDeadline(at) //gosec:disable -- 错误忽略：失败时由 superviseCopies 的硬上限兜底
					_ = outboundConn.SetWriteDeadline(at)           //gosec:disable -- 错误忽略：同上
					if err := injectForwardedHeader(redirection.InboundConn, outboundConn, redirection.ClientIP); err != nil {
						log.Debug("failed to inject X-Forwarded-For header, closing connection:", err)
						return
					}
					_ = redirection.InboundConn.SetReadDeadline(time.Time{}) //gosec:disable -- 错误忽略：解除限时
					_ = outboundConn.SetWriteDeadline(time.Time{})           //gosec:disable -- 错误忽略：解除限时
				}

				var activity atomic.Uint64
				inbound := &activityConn{Conn: redirection.InboundConn, activity: &activity}
				outbound := &activityConn{Conn: outboundConn, activity: &activity}

				// 每个方向退出时投递一个信号。不用 copyWg.Wait()：Wait 无法与 ctx.Done 竞争，
				// 也就无法在关停时被解除；也不用把这两个 goroutine 记进 r.wg，
				// 否则 Close 又要等它们，超时兜底就失效了。
				finished := make(chan struct{}, 2)
				relay := func(dst, src *activityConn) {
					buf := getCopyBuffer()
					defer putCopyBuffer(buf)
					if _, err := io.CopyBuffer(dst, src, buf); err != nil {
						log.Debug(err)
					}
					finished <- struct{}{}
				}
				go relay(outbound, inbound)
				go relay(inbound, outbound)

				// 不能用 "select ctx.Done / default" 判断关闭：ctx 恰已取消时两分支随机命中，
				// 可能误走 default 继续阻塞；superviseCopies 内部是真正的 select
				if r.superviseCopies(finished, inbound, outbound) {
					log.Info("redirection done")
				} else {
					log.Debug("redirector shutting down")
				}
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
