package log

import (
	"fmt"
	"html"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// LogLevel how much log to dump
// 0: ALL; 1: INFO; 2: WARN; 3: ERROR; 4: FATAL; 5: OFF
type LogLevel int

const (
	TraceLevel LogLevel = -1
	DebugLevel LogLevel = 0
	AllLevel   LogLevel = 0
	InfoLevel  LogLevel = 1
	WarnLevel  LogLevel = 2
	ErrorLevel LogLevel = 3
	FatalLevel LogLevel = 4
	OffLevel   LogLevel = 5
)

type Logger interface {
	Fatal(v ...any)
	Fatalf(format string, v ...any)
	Error(v ...any)
	Errorf(format string, v ...any)
	Warn(v ...any)
	Warnf(format string, v ...any)
	Info(v ...any)
	Infof(format string, v ...any)
	Debug(v ...any)
	Debugf(format string, v ...any)
	Trace(v ...any)
	Tracef(format string, v ...any)
	SetLogLevel(level LogLevel)
	SetOutput(io.Writer)
}

var (
	loggerMu sync.RWMutex
	logger   Logger = &EmptyLogger{}
)

// currentLogLevel 是日志级别的镜像，只给 ShouldLog 做「先判级别、再清洗」的短路用。
// 用原子而不是裸变量：它在每个转发 goroutine 里被读，而配置文件 / -api 会在运行期改它。
// 零值 0 恰是 AllLevel，与 logger 的默认行为一致。
var currentLogLevel atomic.Int32

// ShouldLog 报告这个级别的消息会不会真的被输出。级别常量是「越小越啰嗦」。
func ShouldLog(level LogLevel) bool {
	// TraceLevel(-1) 比最啰嗦的合法级别 AllLevel(0) 还小，按大小直接比就恒为 false，
	// 于是 log.Trace/Tracef 的 7 个调用点（router 的规则命中、freedom 拨号直连、
	// tls 握手信息）永远静默——而 golog 内部是按 `logLevel == 0` 输出 Trace 的，
	// 卡住它的只有这一道门禁。Trace 不单独设档：它就是 AllLevel 那一档里最细的部分。
	if level == TraceLevel {
		level = DebugLevel
	}
	return int32(level) >= currentLogLevel.Load()
}

// GetLogLevel 返回当前镜像的级别。
func GetLogLevel() LogLevel {
	return LogLevel(currentLogLevel.Load())
}

func SetLogLevelInternal(level LogLevel) {
	currentLogLevel.Store(int32(level))
}

// getLogger 返回当前 logger 的快照，用于并发安全地读取全局 logger。
func getLogger() Logger {
	loggerMu.RLock()
	defer loggerMu.RUnlock()
	return logger
}

type EmptyLogger struct{}

func (l *EmptyLogger) SetLogLevel(LogLevel) {}

func (l *EmptyLogger) Fatal(v ...any) { os.Exit(1) }

func (l *EmptyLogger) Fatalf(format string, v ...any) { os.Exit(1) }

func (l *EmptyLogger) Error(v ...any) {}

func (l *EmptyLogger) Errorf(format string, v ...any) {}

func (l *EmptyLogger) Warn(v ...any) {}

func (l *EmptyLogger) Warnf(format string, v ...any) {}

func (l *EmptyLogger) Info(v ...any) {}

func (l *EmptyLogger) Infof(format string, v ...any) {}

func (l *EmptyLogger) Debug(v ...any) {}

func (l *EmptyLogger) Debugf(format string, v ...any) {}

func (l *EmptyLogger) Trace(v ...any) {}

func (l *EmptyLogger) Tracef(format string, v ...any) {}

func (l *EmptyLogger) SetOutput(w io.Writer) {}

// 下面这组入口一律先判级别再动手。原因不是风格：SanitizeLogInput 要跑
// 6 条脱敏正则 + 3 次 ReplaceAll + html.EscapeString，getLogger() 还要抢一把
// RWMutex 读锁，而这些开销在过去是无条件的——每条被 logger 自己丢掉的
// Debug/Trace 消息，也先把这份钱花掉了。它落在每包/每连接的路径上
// （如 tunnel/freedom 的 UDP 读路径每包一次 log.Debug），实测一次
// NewAddressFromHostPort 里的 log.Debug 要 11.8µs、28 次分配、958 B。
// Fatal/Fatalf 不设门禁：它们要负责退出进程，短路掉就等于不退出。

// line 把变参实参拼成一行并整行清洗一次；linef 先用**原始实参**完成格式化，再整行清洗一次。
//
// 为什么清洗必须排在格式化之后，而不是像旧写法那样逐个清洗实参再交给 Xxxf：
// SanitizeLogInput 的 switch 只认 string 与 error 两种实参，map / struct / 切片两个分支
// 都不匹配，于是原样穿过格式化落到行缓冲。这不是假想的口子——2026-10-29 是靠 CI 日志
// 坐实的：ConnectionTracker 把 map[string]any 交给 %v，里面 WithField("user", …) 那枚
// SHA224（Trojan 的线上认证物）明文进了日志；同一形状还漏着 websocket 的 Headers、
// transport 的插件 Env 与 cmd.Args。清洗挪到格式化之后，任何被 %v/%+v/%s 拼进文本的字节
// 都只能在整行上过一遍规则，与实参的静态类型无关。
//
// 反方向（逐个清洗后再格式化）不能选：那要把 %.2f/%d 的实参转成字符串，动词失配会打成
// %!f(string=…)。这里格式化用的就是原始类型的值，所以没有这个问题。
//
// 落到行缓冲的字节不变：SanitizeString 会剥掉 Sprintln 带的那个换行，而 golog 的 Output
// 在 data 末尾没有换行时自己补一个。子 logger（golog/simplelog）内部还会再清洗一轮，
// 那是既有行为（旧写法同样跑两轮），[REDACTED] 不会被二次吃掉，但 html 转义本来就非幂等。
func line(v ...any) string {
	return SanitizeString(fmt.Sprintln(v...))
}

func linef(format string, v ...any) string {
	return SanitizeString(fmt.Sprintf(format, v...))
}

func Error(v ...any) {
	if !ShouldLog(ErrorLevel) {
		return
	}
	getLogger().Error(line(v...))
}

func Errorf(format string, v ...any) {
	if !ShouldLog(ErrorLevel) {
		return
	}
	getLogger().Error(linef(format, v...))
}

func Warn(v ...any) {
	if !ShouldLog(WarnLevel) {
		return
	}
	getLogger().Warn(line(v...))
}

func Warnf(format string, v ...any) {
	if !ShouldLog(WarnLevel) {
		return
	}
	getLogger().Warn(linef(format, v...))
}

func Info(v ...any) {
	if !ShouldLog(InfoLevel) {
		return
	}
	getLogger().Info(line(v...))
}

func Infof(format string, v ...any) {
	if !ShouldLog(InfoLevel) {
		return
	}
	getLogger().Info(linef(format, v...))
}

func Debug(v ...any) {
	if !ShouldLog(DebugLevel) {
		return
	}
	// 输入已经过 line() 的整行清洗，CodeQL 无法识别自定义 sanitizer
	// codeql[go/log-injection] ignore
	getLogger().Debug(line(v...))
}

func Debugf(format string, v ...any) {
	if !ShouldLog(DebugLevel) {
		return
	}
	getLogger().Debug(linef(format, v...))
}

func Trace(v ...any) {
	if !ShouldLog(TraceLevel) {
		return
	}
	getLogger().Trace(line(v...))
}

func Tracef(format string, v ...any) {
	if !ShouldLog(TraceLevel) {
		return
	}
	getLogger().Trace(linef(format, v...))
}

func Fatal(v ...any) {
	getLogger().Fatal(line(v...))
}

func Fatalf(format string, v ...any) {
	getLogger().Fatal(linef(format, v...))
}

func SetLogLevel(level LogLevel) {
	// 镜像必须一起更新：ShouldLog 的门禁靠它。此前只有 SetLogLevelInternal 会写镜像
	// （实际只有测试在调），于是 helper.Logf / LogConnectionEvent 的门禁在生产里恒为真
	// —— 级别设成 Info 之后，每条 Debug 仍要走一遍正则脱敏 + RWMutex 读锁。
	SetLogLevelInternal(level)
	getLogger().SetLogLevel(level)
}

func SetOutput(w io.Writer) {
	getLogger().SetOutput(w)
}

func RegisterLogger(l Logger) {
	loggerMu.Lock()
	defer loggerMu.Unlock()
	logger = l
	// 把镜像级别交给新 logger，避免两边级别不一致：否则可能出现「镜像说不用输出、
	// 新 logger 按自己的默认级别照打」或反过来把该输出的短路掉。
	l.SetLogLevel(GetLogLevel())
}

func Logf(level LogLevel, format string, v ...any) {
	if !ShouldLog(level) {
		return
	}
	msg := linef(format, v...)
	l := getLogger()
	switch level {
	case DebugLevel:
		l.Debug(msg)
	case InfoLevel:
		l.Info(msg)
	case WarnLevel:
		l.Warn(msg)
	case ErrorLevel:
		l.Error(msg)
	case FatalLevel:
		l.Fatal(msg)
	}
}

// sensitiveRule 的一条：正则 + 替换串。replacement 里用 ${1} 把键名留住，
// 只把值换成占位符；纯凭据形态（没有键名可留）整段替换。
type sensitiveRule struct {
	re          *regexp.Regexp
	replacement string
}

// 顺序有意义：具体形态必须排在通用兜底（最后那条 32+ 位串）之前。
// 例如 JWT 的 header 段本身就满足通用规则，若先跑通用规则，
// payload/签名两段会留下半截可见。
var SensitivePatterns = []sensitiveRule{
	{regexp.MustCompile(`(?i)(password\s*[=:]\s*)["']?[^\s"']*["']?`), "${1}[REDACTED]"},
	{regexp.MustCompile(`(?i)((?:api[_-]?key|secret[_-]?key|access[_-]?token|auth[_-]?token|bearer[_-]?token)\s*[=:]\s*)["']?[^\s"']*["']?`), "${1}[REDACTED]"},
	{regexp.MustCompile(`(?i)((?:secret|token|credential|passwd)\s*[=:]\s*)["']?[^\s"']*["']?`), "${1}[REDACTED]"},
	{regexp.MustCompile(`[a-f0-9]{40,}`), "[REDACTED]"},
	// 一整段 JWT 连同它的三段一起吃掉（字符类里带 '.'）
	{regexp.MustCompile(`eyJhbGciOiJ[a-zA-Z0-9_.=-]+`), "[REDACTED]"},
	{regexp.MustCompile(`[a-zA-Z0-9]{32,}`), "[REDACTED]"},
}

var SensitiveKeywords = []string{
	"password", "passwd", "secret", "token", "api-key", "apikey",
	"access-token", "accesstoken", "auth-token", "authtoken",
	"credentials", "credential", "private-key", "privatekey", "private_key",
	// 下面几枚是 2026-10-29 那轮审计补的：它们的出现本身就说明这一行在携带凭据，
	// 而里面的值常常短于 SensitivePatterns 的 32 位兜底阈值（authorization 头里的
	// Bearer token、插件参数、连接串都是这种形态）。"proxy-authorization" 不必单列，
	// Contains 判据下已被 "authorization" 覆盖。
	"authorization", "cookie", "dsn", "keyshare",
}

// ObfuscateSensitiveData 遮住凭据，但尽量留下上下文。
//
// 关键词命中仍然整行打码：命中 "password"/"token" 这类词的行通常是配置转储，
// 逐段遮反而容易漏。模式命中改成只替换命中的片段 —— 旧实现里最后那条
// 「32 位以上连续字母数字」会把整行换成 [REDACTED]，实测连
// "tls failed to handshake with https://…长路径…" 和 32 位 trace id 都会被
// 整行抹掉：那不是凭据，而是运维唯一能看到的上下文。
// redactedPlaceholder 是命中内容的替换产物。它自身只有 9 个字母，不会再被后续
// 规则当成凭据重复吃掉。
const redactedPlaceholder = "[REDACTED]"

// safeContextRe 认的是"<标识词><分隔><整整 40 位十六进制>"这一形——git commit SHA 正好 40 位。
//
// 为什么只能靠上下文而不是形状：40 位 hex 与 Trojan 的线上认证物（SHA224，56 位 hex）都是纯
// 十六进制串，而下面那条 [a-f0-9]{40,} 的入口长度就是 40，于是启动横幅的 `Commit: <sha>`
// 被一起吃成 [REDACTED]——运维看不出自己跑的到底是哪个提交，而这正是"到底该不该怪这次改动"
// 唯一能依据的信息。这里只放过**整整 40 位**（结尾的 \b 卡住长度），56 位的 SHA224 和任何更长的
// hex 摘要照旧遮。
var safeContextRe = regexp.MustCompile(`(?i)\b(commit|revision|rev)(\s*[:=]\s*)([a-f0-9]{40})\b`)

// safeMarkByte 是"把安全片段摘出规则射程"用的哨兵字节。它必须同时满足：
//  1. 不会被任何 SensitivePatterns 命中（不是字母数字，也不匹配键名规则）；
//  2. 绝不残留在输出里；
//  3. 先于一切从输入里清掉——否则对端把哨兵连同凭据一起喂进来，就能伪装成"已被保护的片段"
//     绕过脱敏。判据里钉了这条注入。
const safeMarkByte = '\x01'

func safeMark(i int) string {
	return string(safeMarkByte) + strconv.Itoa(i) + string(safeMarkByte)
}

func ObfuscateSensitiveData(str string) string {
	lowerStr := strings.ToLower(str)
	for _, keyword := range SensitiveKeywords {
		if strings.Contains(lowerStr, keyword) {
			return redactedPlaceholder
		}
	}
	// 先把输入里自带的哨字节清掉，再制造哨兵：顺序反了就等于给注入留门。
	out := strings.ReplaceAll(str, string(safeMarkByte), "")
	var saved []string
	out = safeContextRe.ReplaceAllStringFunc(out, func(match string) string {
		groups := safeContextRe.FindStringSubmatch(match)
		saved = append(saved, groups[3])
		return groups[1] + groups[2] + safeMark(len(saved)-1)
	})
	for _, rule := range SensitivePatterns {
		out = rule.re.ReplaceAllString(out, rule.replacement)
	}
	for i, secret := range saved {
		out = strings.ReplaceAll(out, safeMark(i), secret)
	}
	return out
}

func SanitizeString(s string) string {
	s = ObfuscateSensitiveData(s)
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\t", "")
	s = html.EscapeString(s)
	return s
}

// SanitizeLogInput 清洗日志实参。只认 string 是不够的：全仓主流写法是
// log.Error(common.NewError(...).Base(err))，error 不是 string，于是过去原样穿过
// fmt.Sprintln，落到 golog 的行缓冲上（Output 只 Append，不再二次清洗）。
// 后果有两条：① 对端可控字节里的 CR/LF 能伪造整行日志——域名/SNI/地址是
// metadata.go 里长度前缀的任意字节，而 freedom 的拨号错误又会把 addr.String() 拼进
// 错误文案；② 凭据脱敏链一并被绕过（ObfuscateSensitiveData 只在 SanitizeString 里跑）。
func SanitizeLogInput(v []any) []any {
	for i, val := range v {
		switch x := val.(type) {
		case string:
			v[i] = SanitizeString(x)
		case error:
			if x != nil {
				v[i] = SanitizeString(x.Error())
			}
		}
	}
	return v
}
