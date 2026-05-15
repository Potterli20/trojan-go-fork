package log

import (
	"html"
	"io"
	"os"
	"regexp"
	"strings"
)

// LogLevel how much log to dump
// 0: ALL; 1: INFO; 2: WARN; 3: ERROR; 4: FATAL; 5: OFF
type LogLevel int

const (
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

var logger Logger = &EmptyLogger{}

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

func Error(v ...any) {
	logger.Error(SanitizeLogInput(v)...)
}

func Errorf(format string, v ...any) {
	logger.Errorf(SanitizeString(format), SanitizeLogInput(v)...)
}

func Warn(v ...any) {
	logger.Warn(SanitizeLogInput(v)...)
}

func Warnf(format string, v ...any) {
	logger.Warnf(SanitizeString(format), SanitizeLogInput(v)...)
}

func Info(v ...any) {
	logger.Info(SanitizeLogInput(v)...)
}

func Infof(format string, v ...any) {
	logger.Infof(SanitizeString(format), SanitizeLogInput(v)...)
}

func Debug(v ...any) {
	logger.Debug(SanitizeLogInput(v)...)
}

func Debugf(format string, v ...any) {
	logger.Debugf(SanitizeString(format), SanitizeLogInput(v)...)
}

func Trace(v ...any) {
	logger.Trace(SanitizeLogInput(v)...)
}

func Tracef(format string, v ...any) {
	logger.Tracef(SanitizeString(format), SanitizeLogInput(v)...)
}

func Fatal(v ...any) {
	logger.Fatal(SanitizeLogInput(v)...)
}

func Fatalf(format string, v ...any) {
	logger.Fatalf(SanitizeString(format), SanitizeLogInput(v)...)
}

func SetLogLevel(level LogLevel) {
	logger.SetLogLevel(level)
}

func SetOutput(w io.Writer) {
	logger.SetOutput(w)
}

func RegisterLogger(l Logger) {
	logger = l
}

var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)password\s*[=:]\s*["']?[^"'\\\s]+["']?`),
	regexp.MustCompile(`(?i)(api[_-]?key|secret[_-]?key|access[_-]?token|auth[_-]?token|bearer[_-]?token)\s*[=:]\s*["']?[^"'\\\s]+["']?`),
	regexp.MustCompile(`(?i)(secret|token|credential|passwd)\s*[=:]\s*["']?[^"'\\\s]+["']?`),
	regexp.MustCompile(`[a-f0-9]{40}`),
	regexp.MustCompile(`eyJhbGciOiJ[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+`),
	regexp.MustCompile(`[a-zA-Z0-9]{32,}`),
}

var sensitiveKeywords = []string{
	"password", "passwd", "secret", "token", "api-key", "apikey",
	"access-token", "accesstoken", "auth-token", "authtoken",
	"credentials", "credential", "private-key", "privatekey",
}

func obfuscateSensitiveData(str string) string {
	lowerStr := strings.ToLower(str)
	for _, keyword := range sensitiveKeywords {
		if strings.Contains(lowerStr, keyword) {
			return "[REDACTED]"
		}
	}
	for _, pattern := range sensitivePatterns {
		if pattern.MatchString(str) {
			return "[REDACTED]"
		}
	}
	return str
}

func SanitizeString(s string) string {
	s = obfuscateSensitiveData(s)
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
