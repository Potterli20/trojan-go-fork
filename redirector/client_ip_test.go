package redirector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
)

// TestClientIPSwitchIsParsedFromConfigText 钉的是"这个键真的接上了"。
// 上面的用例直接构造 Config 结构体塞进 ctx，那样哪怕 json/yaml 标签写错、
// 或者压根忘了 RegisterConfigCreator，判据照样绿——本会话在 quic 配置上就踩过
// 这种「代码写了但没接线」。这里走真实的解析入口，两种格式各来一遍，
// 并额外钉一条「文档里没有这个键时必须是关」。
func TestClientIPSwitchIsParsedFromConfigText(t *testing.T) {
	cases := []struct {
		name string
		json bool
		text string
		want bool
	}{
		{"yaml 打开", false, "redirect-client-ip: true\n", true},
		{"json 打开", true, `{"redirect_client_ip": true}`, true},
		{"yaml 显式关", false, "redirect-client-ip: false\n", false},
		{"文档里没有这个键", false, "run-type: server\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var ctx context.Context
			var err error
			if c.json {
				ctx, err = config.WithJSONConfig(context.Background(), []byte(c.text))
			} else {
				ctx, err = config.WithYAMLConfig(context.Background(), []byte(c.text))
			}
			if err != nil {
				t.Fatalf("解析配置失败: %v", err)
			}
			if got := injectClientIPIsEnabled(ctx); got != c.want {
				t.Fatalf("开关状态 = %v，期望 %v（键名或注册没接上）", got, c.want)
			}
		})
	}
}

// honeypot 是诱饵/回落服务器：收一条重定向进来的连接，把收到的字节攒下来，
// 并且可以往回写（用来证明注入阶段之后双向中继还活着）。
type honeypot struct {
	ln   net.Listener
	conn chan net.Conn
}

func startHoneypot(t *testing.T) *honeypot {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	common.Must(err)
	h := &honeypot{ln: ln, conn: make(chan net.Conn, 8)}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			h.conn <- c
		}
	}()
	return h
}

// collect 收下第一条重定向连接，攒字节直到"读满时限内没有新动静"或看到 want。
//
// 收尾用静默而不是 EOF，是因为下面的判据要证明注入阶段之后连接仍然开着：
// 实现若把连接关了，Read 会拿到 EOF（不是超时），这里就把它当失败返回。
// 返回的 conn 交给调用方继续往回写，避免再去 chan 里等第二条连接。
func (h *honeypot) collect(t *testing.T, want string, budget time.Duration) (string, net.Conn, error) {
	t.Helper()
	c, err := h.waitConn(budget)
	if err != nil {
		return "", nil, err
	}
	var sb strings.Builder
	buf := make([]byte, 4096)
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond)) //gosec:disable -- 错误忽略：测试夹具
		n, err := c.Read(buf)
		sb.Write(buf[:n])
		if err == nil {
			if want != "" && strings.Contains(sb.String(), want) {
				return sb.String(), c, nil
			}
			continue
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			continue // 只是暂时没动静
		}
		return sb.String(), c, err // EOF/重置等：连接已经不行了
	}
	return sb.String(), c, nil
}

func (h *honeypot) waitConn(budget time.Duration) (net.Conn, error) {
	select {
	case c := <-h.conn:
		return c, nil
	case <-time.After(budget):
		return nil, fmt.Errorf("诱饵服务器在 %v 内没有收到重定向连接", budget)
	}
}

// probeConn 造一条真实 TCP 入站连接，返回（客户端侧、交给 redirector 的服务端侧）。
// 用真实 TCP 而不是 net.Pipe 是为了 RemoteAddr 带端口，能顺带验 hostOnly 的取址。
func probeConn(t *testing.T) (client net.Conn, inbound net.Conn, clientHost string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	common.Must(err)
	c, err := net.Dial("tcp", ln.Addr().String())
	common.Must(err)
	s, err := ln.Accept()
	common.Must(err)
	host, _, err := net.SplitHostPort(s.RemoteAddr().String())
	common.Must(err)
	t.Cleanup(func() {
		c.Close()  //gosec:disable -- 错误忽略：测试清理
		s.Close()  //gosec:disable -- 错误忽略：测试清理
		ln.Close() //gosec:disable -- 错误忽略：测试清理
	})
	return c, s, host
}

func newRedirector(t *testing.T, enabled bool) *Redirector {
	t.Helper()
	var ctx context.Context
	if enabled {
		ctx = config.WithConfig(context.Background(), Name, &Config{RedirectClientIP: true})
	} else {
		ctx = context.Background()
	}
	r := NewRedirector(ctx)
	t.Cleanup(func() { go r.Close() }) //gosec:disable -- 错误忽略：测试清理
	return r
}

func redirectTo(h *honeypot) net.Addr {
	addr, err := net.ResolveTCPAddr("tcp", h.ln.Addr().String())
	common.Must(err)
	return addr
}

// TestClientIPNotInjectedByDefault 是"默认关"这条裁定的判据：不开配置时，
// 诱饵收到的必须与客户端发的完全一致，一个字节都不能多。
// 这条在实现前后都必须是绿的（它就是旧行为），否则"默认关"只是嘴上说的。
func TestClientIPNotInjectedByDefault(t *testing.T) {
	h := startHoneypot(t)
	defer h.ln.Close() //gosec:disable -- 错误忽略：测试清理
	client, inbound, _ := probeConn(t)
	r := newRedirector(t, false)

	r.Redirect(&Redirection{RedirectTo: redirectTo(h), InboundConn: inbound})
	_, err := io.WriteString(client, "GET /x HTTP/1.1\r\nHost: probe.example\r\n\r\n")
	common.Must(err)

	got, _, err := h.collect(t, "Host: probe.example", 3*time.Second)
	if err != nil {
		t.Fatalf("诱饵侧读取失败: %v", err)
	}
	if strings.Contains(strings.ToLower(got), "x-forwarded-for") || strings.Contains(strings.ToLower(got), "x-real-ip") {
		t.Fatalf("没有开 redirect-client-ip，却还是注入了客户端地址：\n%s", got)
	}
	if got != "GET /x HTTP/1.1\r\nHost: probe.example\r\n\r\n" {
		t.Fatalf("默认关闭时字节应当原样中继，实际 %q", got)
	}
}

// TestClientIPInjectedWhenEnabled 是主判据：打开以后，X-Forwarded-For 与
// X-Real-IP 都带上客户端地址，且值是"主机"而不是"主机:端口"。
func TestClientIPInjectedWhenEnabled(t *testing.T) {
	h := startHoneypot(t)
	defer h.ln.Close() //gosec:disable -- 错误忽略：测试清理
	client, inbound, clientHost := probeConn(t)
	r := newRedirector(t, true)

	r.Redirect(&Redirection{RedirectTo: redirectTo(h), InboundConn: inbound})
	_, err := io.WriteString(client, "POST /up HTTP/1.1\r\nHost: probe.example\r\nContent-Length: 11\r\n\r\nhello\r\nworld")
	common.Must(err)

	got, _, err := h.collect(t, "X-Real-IP", 3*time.Second)
	if err != nil {
		t.Fatalf("诱饵侧读取失败: %v", err)
	}
	wantXF := "X-Forwarded-For: " + clientHost + "\r\n"
	wantReal := "X-Real-IP: " + clientHost + "\r\n"
	if !strings.Contains(got, wantXF) {
		t.Fatalf("缺少 %q，实际：\n%s", wantXF, got)
	}
	if !strings.Contains(got, wantReal) {
		t.Fatalf("缺少 %q，实际：\n%s", wantReal, got)
	}
	if strings.Contains(got, wantXF[:len(wantXF)-2]+":") {
		t.Fatalf("XFF 里带着端口，不是合法地址值：\n%s", got)
	}
	// 头部块之后的请求体不能被注入过程吃掉或重复
	if !strings.HasSuffix(got, "hello\r\nworld") {
		t.Fatalf("请求体被截断或改坏了，实际：%q", got)
	}
}

// TestExistingClientIPHeaderIsAppended 守住追加语义：探测者自己带来的 XFF
// 不被替换，客户端地址接在后面（诱饵侧才看得出经过了谁）。
func TestExistingClientIPHeaderIsAppended(t *testing.T) {
	h := startHoneypot(t)
	defer h.ln.Close() //gosec:disable -- 错误忽略：测试清理
	client, inbound, clientHost := probeConn(t)
	r := newRedirector(t, true)

	r.Redirect(&Redirection{RedirectTo: redirectTo(h), InboundConn: inbound})
	_, err := io.WriteString(client, "GET /x HTTP/1.1\r\nHost: probe.example\r\nX-Forwarded-For: 203.0.113.7\r\n\r\n")
	common.Must(err)

	got, _, err := h.collect(t, "X-Real-IP", 3*time.Second)
	if err != nil {
		t.Fatalf("诱饵侧读取失败: %v", err)
	}
	if !strings.Contains(got, "X-Forwarded-For: 203.0.113.7, "+clientHost) {
		t.Fatalf("已有的 XFF 应被追加而不是替换，实际：\n%s", got)
	}
}

// TestNonHTTPPayloadIsRelayedAndKeptAlive 是本轮裁定的核心，也是旧实现真正会坏的地方：
// 载荷里有 CRLFCRLF 但不是 HTTP 请求（裸协议探测很常见），旧代码会往里塞 XFF 行、
// 把那个协议自己的字节改坏；而"读不出头部块"的分支更是直接关掉连接。
// 诱饵的作用就是让探测者以为撞上了真服务，掐连接等于自曝。
func TestNonHTTPPayloadIsRelayedAndKeptAlive(t *testing.T) {
	h := startHoneypot(t)
	defer h.ln.Close() //gosec:disable -- 错误忽略：测试清理
	client, inbound, _ := probeConn(t)
	r := newRedirector(t, true)

	r.Redirect(&Redirection{RedirectTo: redirectTo(h), InboundConn: inbound})
	// 有头部边界，但请求行不合法
	_, err := io.WriteString(client, "GARBAGE\r\n\r\n")
	common.Must(err)

	const more = "MORE-PROBE-BYTES"
	go func() {
		time.Sleep(400 * time.Millisecond)
		io.WriteString(client, more) //gosec:disable -- 错误忽略：测试夹具
	}()

	got, hpConn, err := h.collect(t, more, 3*time.Second)
	if err != nil {
		t.Fatalf("诱饵侧读取失败: %v（连接若被掐断，这里会拿到 EOF）", err)
	}
	if strings.Contains(strings.ToLower(got), "x-forwarded-for") {
		t.Fatalf("非 HTTP 载荷被注入了请求头：\n%s", got)
	}
	if !strings.Contains(got, "GARBAGE\r\n\r\n") || !strings.Contains(got, more) {
		t.Fatalf("非 HTTP 载荷没有原样中继：\n%q", got)
	}

	// 反向也必须还通：诱饵写回去的东西客户端要收得到
	if _, err := hpConn.Write([]byte("PONG-FROM-HONEYPOT")); err != nil {
		t.Fatalf("诱饵侧写入失败: %v", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("注入阶段之后反向中继断了: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "PONG-FROM-HONEYPOT") {
		t.Fatalf("客户端收到的回包不对: %q", string(buf[:n]))
	}
}

// TestHeadTooLargeIsRelayedNotDropped 覆盖注入阶段的另一个出口：探测者狂发
// 非 HTTP 字节超过 64KB 上限时，读到的部分必须照样交给诱饵，连接不能断
// （拿到 EOF 也算失败，见下面对 errors.Is(err, io.EOF) 的那句解释）。
func TestHeadTooLargeIsRelayedNotDropped(t *testing.T) {
	h := startHoneypot(t)
	defer h.ln.Close() //gosec:disable -- 错误忽略：测试清理
	client, inbound, _ := probeConn(t)
	r := newRedirector(t, true)

	r.Redirect(&Redirection{RedirectTo: redirectTo(h), InboundConn: inbound})
	go func() {
		blob := strings.Repeat("Z", 70*1024) // 没有 CRLFCRLF，逼出 64KB 上限那条出口
		io.WriteString(client, blob)         //gosec:disable -- 错误忽略：测试夹具
	}()

	got, hpConn, err := h.collect(t, "", 4*time.Second)
	if err != nil {
		t.Fatalf("诱饵侧读取失败（实现若像旧版那样掐掉会话，这里会拿到 EOF）: %v", err)
	}
	if len(got) < 60*1024 {
		t.Fatalf("超过头部上限的探测字节被丢了，只收到 %d 字节", len(got))
	}
	if strings.Contains(strings.ToLower(got), "x-forwarded-for") {
		t.Fatalf("不是 HTTP 请求，却注入了请求头")
	}

	// 会话必须还活着：诱饵回写，客户端要收得到。旧实现在这条出口上是直接
	// return 的（defer 关掉两端），所以这一段就是它与新实现的分水岭。
	if _, err := hpConn.Write([]byte("PONG-AFTER-LIMIT")); err != nil {
		t.Fatalf("超限出口之后诱饵侧已写不进去（会话被掐断）: %v", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("超限出口之后反向中继已断: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "PONG-AFTER-LIMIT") {
		t.Fatalf("客户端收到的回包不对: %q", string(buf[:n]))
	}
}
