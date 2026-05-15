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
	logger   *golog.Logger
}

func (l *SimpleLogger) SetLogLevel(level log.LogLevel) {
	l.logLevel = level
}

func (l *SimpleLogger) Fatal(v ...any) {
	if l.logLevel <= log.FatalLevel {
		if l.logger != nil {
			l.logger.Fatalln(sanitizeLogInput(v)...)
		} else {
			golog.Fatal(sanitizeLogInput(v)...)
		}
	}
	os.Exit(1)
}

func (l *SimpleLogger) Fatalf(format string, v ...any) {
	if l.logLevel <= log.FatalLevel {
		if l.logger != nil {
			l.logger.Fatalf(sanitizeString(format), sanitizeLogInput(v)...)
		} else {
			golog.Fatalf(sanitizeString(format), sanitizeLogInput(v)...)
		}
	}
	os.Exit(1)
}

func (l *SimpleLogger) Error(v ...any) {
	if l.logLevel <= log.ErrorLevel {
		if l.logger != nil {
			l.logger.Println(sanitizeLogInput(v)...)
		} else {
			golog.Println(sanitizeLogInput(v)...)
		}
	}
}

func (l *SimpleLogger) Errorf(format string, v ...any) {
	if l.logLevel <= log.ErrorLevel {
		if l.logger != nil {
			l.logger.Printf(sanitizeString(format), sanitizeLogInput(v)...)
		} else {
			golog.Printf(sanitizeString(format), sanitizeLogInput(v)...)
		}
	}
}

func (l *SimpleLogger) Warn(v ...any) {
	if l.logLevel <= log.WarnLevel {
		if l.logger != nil {
			l.logger.Println(sanitizeLogInput(v)...)
		} else {
			golog.Println(sanitizeLogInput(v)...)
		}
	}
}

func (l *SimpleLogger) Warnf(format string, v ...any) {
	if l.logLevel <= log.WarnLevel {
		if l.logger != nil {
			l.logger.Printf(sanitizeString(format), sanitizeLogInput(v)...)
		} else {
			golog.Printf(sanitizeString(format), sanitizeLogInput(v)...)
		}
	}
}

func (l *SimpleLogger) Info(v ...any) {
	if l.logLevel <= log.InfoLevel {
		if l.logger != nil {
			l.logger.Println(sanitizeLogInput(v)...)
		} else {
			golog.Println(sanitizeLogInput(v)...)
		}
	}
}

func (l *SimpleLogger) Infof(format string, v ...any) {
	if l.logLevel <= log.InfoLevel {
		if l.logger != nil {
			l.logger.Printf(sanitizeString(format), sanitizeLogInput(v)...)
		} else {
			golog.Printf(sanitizeString(format), sanitizeLogInput(v)...)
		}
	}
}

func (l *SimpleLogger) Debug(v ...any) {
	if l.logLevel <= log.AllLevel {
		if l.logger != nil {
			l.logger.Println(sanitizeLogInput(v)...)
		} else {
			golog.Println(sanitizeLogInput(v)...)
		}
	}
}

func (l *SimpleLogger) Debugf(format string, v ...any) {
	if l.logLevel <= log.AllLevel {
		if l.logger != nil {
			l.logger.Printf(sanitizeString(format), sanitizeLogInput(v)...)
		} else {
			golog.Printf(sanitizeString(format), sanitizeLogInput(v)...)
		}
	}
}

func (l *SimpleLogger) Trace(v ...any) {
	if l.logLevel <= log.AllLevel {
		if l.logger != nil {
			l.logger.Println(sanitizeLogInput(v)...)
		} else {
			golog.Println(sanitizeLogInput(v)...)
		}
	}
}

func (l *SimpleLogger) Tracef(format string, v ...any) {
	if l.logLevel <= log.AllLevel {
		if l.logger != nil {
			l.logger.Printf(sanitizeString(format), sanitizeLogInput(v)...)
		} else {
			golog.Printf(sanitizeString(format), sanitizeLogInput(v)...)
		}
	}
}

func obfuscateSensitiveData(v []any) []any {
	for i, val := range v {
		if str, ok := val.(string); ok {
			if log.ObfuscateSensitiveData(str) == "[REDACTED]" {
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

func (l *SimpleLogger) SetOutput(w io.Writer) {
	l.logger = golog.New(w, "", 0)
}
