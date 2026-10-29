package log

import (
	"errors"
	"strings"
	"testing"
)

// 56 位十六进制 SHA224。对 Trojan 来说这不是"密码的哈希所以泄露了也无所谓"，
// 而是线上直接拿去认证的那串——握有它就等于握有这个用户。
// 取 2026-10-29 CI 日志里真实出现过的那一枚。
const redactTestUserHash = "7e6a4309ddf6e8866679f61ace4f621b0e3455ebac2e831a60f13cd1"

// Success/Error 把 fields 交给 %v 之前必须先转成已清洗的字符串。
//
// fields 的静态类型是 map[string]any，而 SanitizeLogInput 只改写 string 与 error 两种实参：
// map 两个分支都不匹配，于是原样穿过 fmt 落到行缓冲——tunnel/trojan/client.go 正是用
// WithField("user", c.user.GetHash()) 把上面那枚 hash 塞进去的，默认级别又是 InfoLevel，
// 于是每条出站连接都在日志里明文漏一次凭据。走 string 实参的同类调用
// （log.Info("Using user hash:", …)）本来就被遮成 [REDACTED]，漏的只有 map 这一条路。
//
// 正向对照：把 sanitizedFields 换回 t.fields，这两条用例立刻在"hash 明文进了日志"处变红。
func TestTrackerSuccessRedactsCredentialField(t *testing.T) {
	useLevel(t, AllLevel)
	l := &sliceLogger{}
	useLogger(t, l)

	tracker := NewConnectionTracker("Trojan", "DialConn").
		WithField("target", "127.0.0.1:46857").
		WithField("user", redactTestUserHash)
	if err := tracker.Success(); err != nil {
		t.Fatalf("Success 不该返回错误：%v", err)
	}
	line := strings.Join(l.take(), "\n")

	if strings.Contains(line, redactTestUserHash) {
		t.Fatalf("user hash 明文进了日志（每条连接漏一次）：%q", line)
	}
	if !strings.Contains(line, redactedPlaceholder) {
		t.Fatalf("脱敏链没命中，日志里没有占位符：%q", line)
	}
	// 只该遮命中的那一段：非凭据字段是排障唯一能看到的上下文，不能一起吞掉。
	if !strings.Contains(line, "127.0.0.1:46857") {
		t.Fatalf("target 字段被一起弄丢了：%q", line)
	}
	// 这条钉住"为什么不在 SanitizeLogInput 里一刀切转成字符串"：
	// 那样 durationMs 的 %.2f 会收到 string 而打出 %!f。
	if strings.Contains(line, "%!") {
		t.Fatalf("格式化动词失配（数值实参不该被转成字符串）：%q", line)
	}
}

func TestTrackerErrorRedactsCredentialField(t *testing.T) {
	useLevel(t, AllLevel)
	l := &sliceLogger{}
	useLogger(t, l)

	tracker := NewConnectionTracker("Trojan", "DialConn").
		WithField("sni", "localhost").
		WithField("user", redactTestUserHash)
	sentinel := errors.New("handshake refused")
	if err := tracker.Error(sentinel); err != sentinel {
		t.Fatalf("Error 必须把原错误透传回来：%v", err)
	}
	line := strings.Join(l.take(), "\n")

	if strings.Contains(line, redactTestUserHash) {
		t.Fatalf("错误出口同样漏凭据：%q", line)
	}
	if !strings.Contains(line, "handshake refused") {
		t.Fatalf("error 实参不该被清洗掉内容：%q", line)
	}
	if !strings.Contains(line, "localhost") {
		t.Fatalf("sni 字段被一起弄丢了：%q", line)
	}
}

// 没有 fields 时输出形态不能变：旧写法是 fmt.Sprint(nil map) → "map[]"，
// 而 fields 是延迟分配的（tracker_lazy_fields_test.go 钉的就是这条），
// 默认路径上每条连接都走这个分支。
func TestTrackerWithoutFieldsKeepsOldShape(t *testing.T) {
	useLevel(t, AllLevel)
	l := &sliceLogger{}
	useLogger(t, l)

	if err := NewConnectionTracker("Transport", "DialConn").Success(); err != nil {
		t.Fatalf("Success 不该返回错误：%v", err)
	}
	line := strings.Join(l.take(), "\n")
	if !strings.HasSuffix(line, "map[]") {
		t.Fatalf("空 fields 的出口变了形（会让既有的日志解析/告警规则失配）：%q", line)
	}
}
