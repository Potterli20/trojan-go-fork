package log

import (
	"html"
	"io"
	"os"
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
	logger.Error(sanitizeLogInput(v)...)
}

func Errorf(format string, v ...any) {
	logger.Errorf(sanitizeString(format), sanitizeLogInput(v)...)
}

func Warn(v ...any) {
	logger.Warn(sanitizeLogInput(v)...)
}

func Warnf(format string, v ...any) {
	logger.Warnf(sanitizeString(format), sanitizeLogInput(v)...)
}

func Info(v ...any) {
	logger.Info(sanitizeLogInput(v)...)
}

func Infof(format string, v ...any) {
	logger.Infof(sanitizeString(format), sanitizeLogInput(v)...)
}

func Debug(v ...any) {
	logger.Debug(sanitizeLogInput(v)...)
}

func Debugf(format string, v ...any) {
	logger.Debugf(sanitizeString(format), sanitizeLogInput(v)...)
}

func Trace(v ...any) {
	logger.Trace(sanitizeLogInput(v)...)
}

func Tracef(format string, v ...any) {
	logger.Tracef(sanitizeString(format), sanitizeLogInput(v)...)
}

func Fatal(v ...any) {
	logger.Fatal(sanitizeLogInput(v)...)
}

func Fatalf(format string, v ...any) {
	logger.Fatalf(sanitizeString(format), sanitizeLogInput(v)...)
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

func sanitizeString(s string) string {
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\t", "")
	s = html.EscapeString(s)
	return s
}

func sanitizeLogInput(v []any) []any {
	for i, val := range v {
		if str, ok := val.(string); ok {
			v[i] = sanitizeString(str)
		}
	}
	return v
}
