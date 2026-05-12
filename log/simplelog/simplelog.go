package simplelog

import (
	"html"
	"io"
	golog "log"
	"os"
	"strings"

	"github.com/Potterli20/trojan-go-fork/log"
)

func init() {
	log.RegisterLogger(&SimpleLogger{})
}

type SimpleLogger struct {
	logLevel log.LogLevel
}

func (l *SimpleLogger) SetLogLevel(level log.LogLevel) {
	l.logLevel = level
}

func (l *SimpleLogger) Fatal(v ...any) {
	if l.logLevel <= log.FatalLevel {
		golog.Fatal(sanitizeLogInput(v)...)
	}
	os.Exit(1)
}

func (l *SimpleLogger) Fatalf(format string, v ...any) {
	if l.logLevel <= log.FatalLevel {
		golog.Fatalf(sanitizeString(format), sanitizeLogInput(v)...)
	}
	os.Exit(1)
}

func (l *SimpleLogger) Error(v ...any) {
	if l.logLevel <= log.ErrorLevel {
		golog.Println(sanitizeLogInput(v)...)
	}
}

func (l *SimpleLogger) Errorf(format string, v ...any) {
	if l.logLevel <= log.ErrorLevel {
		golog.Printf(sanitizeString(format), sanitizeLogInput(v)...)
	}
}

func (l *SimpleLogger) Warn(v ...any) {
	if l.logLevel <= log.WarnLevel {
		golog.Println(sanitizeLogInput(v)...)
	}
}

func (l *SimpleLogger) Warnf(format string, v ...any) {
	if l.logLevel <= log.WarnLevel {
		golog.Printf(sanitizeString(format), sanitizeLogInput(v)...)
	}
}

func (l *SimpleLogger) Info(v ...any) {
	if l.logLevel <= log.InfoLevel {
		golog.Println(sanitizeLogInput(v)...)
	}
}

func (l *SimpleLogger) Infof(format string, v ...any) {
	if l.logLevel <= log.InfoLevel {
		golog.Printf(sanitizeString(format), sanitizeLogInput(v)...)
	}
}

func (l *SimpleLogger) Debug(v ...any) {
	if l.logLevel <= log.AllLevel {
		golog.Println(sanitizeLogInput(v)...)
	}
}

func (l *SimpleLogger) Debugf(format string, v ...any) {
	if l.logLevel <= log.AllLevel {
		golog.Printf(sanitizeString(format), sanitizeLogInput(v)...)
	}
}

func (l *SimpleLogger) Trace(v ...any) {
	if l.logLevel <= log.AllLevel {
		golog.Println(sanitizeLogInput(v)...)
	}
}

func (l *SimpleLogger) Tracef(format string, v ...any) {
	if l.logLevel <= log.AllLevel {
		golog.Printf(sanitizeString(format), sanitizeLogInput(v)...)
	}
}

func obfuscateSensitiveData(v []any) []any {
	for i, val := range v {
		if str, ok := val.(string); ok {
			if strings.Contains(strings.ToLower(str), "password") {
				v[i] = "[REDACTED]"
			}
		}
	}
	return v
}

func sanitizeLogInput(v []any) []any {
	v = obfuscateSensitiveData(v)
	for i, val := range v {
		if str, ok := val.(string); ok {
			str = sanitizeString(str)
			v[i] = str
		}
	}
	return v
}

func sanitizeString(s string) string {
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\t", "")
	s = html.EscapeString(s)
	return s
}

func (l *SimpleLogger) SetOutput(io.Writer) {
}
