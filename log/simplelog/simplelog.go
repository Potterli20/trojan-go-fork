package simplelog

import (
	"io"
	golog "log"
	"os"
	"strings"
	"html"

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
		golog.Fatalf(format, sanitizeLogInput(v)...)
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
		golog.Printf(format, sanitizeLogInput(v)...)
	}
}

func (l *SimpleLogger) Warn(v ...any) {
	if l.logLevel <= log.WarnLevel {
		golog.Println(sanitizeLogInput(v)...)
	}
}

func (l *SimpleLogger) Warnf(format string, v ...any) {
	if l.logLevel <= log.WarnLevel {
		golog.Printf(format, sanitizeLogInput(v)...)
	}
}

func (l *SimpleLogger) Info(v ...any) {
	if l.logLevel <= log.InfoLevel {
		golog.Println(sanitizeLogInput(v)...)
	}
}

func (l *SimpleLogger) Infof(format string, v ...any) {
	if l.logLevel <= log.InfoLevel {
		golog.Printf(format, sanitizeLogInput(v)...)
	}
}

func (l *SimpleLogger) Debug(v ...any) {
	if l.logLevel <= log.AllLevel {
		golog.Println(sanitizeLogInput(v)...)
	}
}

func (l *SimpleLogger) Debugf(format string, v ...any) {
	if l.logLevel <= log.AllLevel {
		golog.Printf(format, sanitizeLogInput(v)...)
	}
}

func (l *SimpleLogger) Trace(v ...any) {
	if l.logLevel <= log.AllLevel {
		golog.Println(sanitizeLogInput(v)...)
	}
}

func (l *SimpleLogger) Tracef(format string, v ...any) {
	if l.logLevel <= log.AllLevel {
		golog.Printf(format, sanitizeLogInput(v)...)
	}
}

func sanitizeLogInput(v []any) []any {
	for i, val := range v {
		if str, ok := val.(string); ok {
			// Remove newline and carriage return characters
			str = strings.ReplaceAll(strings.ReplaceAll(str, "\n", ""), "\r", "")
			// Escape HTML special characters
			str = html.EscapeString(str)
			v[i] = str
		}
	}
	return v
}

func (l *SimpleLogger) SetOutput(io.Writer) {
	// do nothing
}
