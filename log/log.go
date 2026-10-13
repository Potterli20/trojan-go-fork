package log

import (
	"html"
	"io"
	"os"
	"regexp"
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

func Error(v ...any) {
	if !ShouldLog(ErrorLevel) {
		return
	}
	getLogger().Error(SanitizeLogInput(v)...)
}

func Errorf(format string, v ...any) {
	if !ShouldLog(ErrorLevel) {
		return
	}
	getLogger().Errorf(SanitizeString(format), SanitizeLogInput(v)...)
}

func Warn(v ...any) {
	if !ShouldLog(WarnLevel) {
		return
	}
	getLogger().Warn(SanitizeLogInput(v)...)
}

func Warnf(format string, v ...any) {
	if !ShouldLog(WarnLevel) {
		return
	}
	getLogger().Warnf(SanitizeString(format), SanitizeLogInput(v)...)
}

func Info(v ...any) {
	if !ShouldLog(InfoLevel) {
		return
	}
	getLogger().Info(SanitizeLogInput(v)...)
}

func Infof(format string, v ...any) {
	if !ShouldLog(InfoLevel) {
		return
	}
	getLogger().Infof(SanitizeString(format), SanitizeLogInput(v)...)
}

func Debug(v ...any) {
	if !ShouldLog(DebugLevel) {
		return
	}
	// 输入已经过 SanitizeLogInput 清洗，CodeQL 无法识别自定义 sanitizer
	// codeql[go/log-injection] ignore
	getLogger().Debug(SanitizeLogInput(v)...)
}

func Debugf(format string, v ...any) {
	if !ShouldLog(DebugLevel) {
		return
	}
	getLogger().Debugf(SanitizeString(format), SanitizeLogInput(v)...)
}

func Trace(v ...any) {
	if !ShouldLog(TraceLevel) {
		return
	}
	getLogger().Trace(SanitizeLogInput(v)...)
}

func Tracef(format string, v ...any) {
	if !ShouldLog(TraceLevel) {
		return
	}
	getLogger().Tracef(SanitizeString(format), SanitizeLogInput(v)...)
}

func Fatal(v ...any) {
	getLogger().Fatal(SanitizeLogInput(v)...)
}

func Fatalf(format string, v ...any) {
	getLogger().Fatalf(SanitizeString(format), SanitizeLogInput(v)...)
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
	sanitizedFormat := SanitizeString(format)
	sanitized := SanitizeLogInput(v)
	l := getLogger()
	switch level {
	case DebugLevel:
		l.Debugf(sanitizedFormat, sanitized...)
	case InfoLevel:
		l.Infof(sanitizedFormat, sanitized...)
	case WarnLevel:
		l.Warnf(sanitizedFormat, sanitized...)
	case ErrorLevel:
		l.Errorf(sanitizedFormat, sanitized...)
	case FatalLevel:
		l.Fatalf(sanitizedFormat, sanitized...)
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
	"credentials", "credential", "private-key", "privatekey",
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

func ObfuscateSensitiveData(str string) string {
	lowerStr := strings.ToLower(str)
	for _, keyword := range SensitiveKeywords {
		if strings.Contains(lowerStr, keyword) {
			return redactedPlaceholder
		}
	}
	out := str
	for _, rule := range SensitivePatterns {
		out = rule.re.ReplaceAllString(out, rule.replacement)
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

func SanitizeLogInput(v []any) []any {
	for i, val := range v {
		if str, ok := val.(string); ok {
			v[i] = SanitizeString(str)
		}
	}
	return v
}
