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

func sanitizeInput(v ...any) []any {
	result := make([]any, len(v))
	for i, val := range v {
		if str, ok := val.(string); ok {
			str = strings.ReplaceAll(str, "\n", "")
			str = strings.ReplaceAll(str, "\r", "")
			str = strings.ReplaceAll(str, "\t", "")
			str = html.EscapeString(str)
			str = log.ObfuscateSensitiveData(str)
			result[i] = str
		} else {
			result[i] = val
		}
	}
	return result
}

func sanitizeFormat(format string) string {
	format = strings.ReplaceAll(format, "\n", "")
	format = strings.ReplaceAll(format, "\r", "")
	format = strings.ReplaceAll(format, "\t", "")
	format = html.EscapeString(format)
	format = log.ObfuscateSensitiveData(format)
	return format
}

func (l *SimpleLogger) Fatal(v ...any) {
	if l.logLevel <= log.FatalLevel {
		sanitized := sanitizeInput(v...)
		if l.logger != nil {
			l.logger.Fatalln(sanitized...)
		} else {
			golog.Fatal(sanitized...)
		}
	}
	os.Exit(1)
}

func (l *SimpleLogger) Fatalf(format string, v ...any) {
	if l.logLevel <= log.FatalLevel {
		sanitizedFormat := sanitizeFormat(format)
		sanitized := sanitizeInput(v...)
		if l.logger != nil {
			l.logger.Fatalf(sanitizedFormat, sanitized...)
		} else {
			golog.Fatalf(sanitizedFormat, sanitized...)
		}
	}
	os.Exit(1)
}

func (l *SimpleLogger) Error(v ...any) {
	if l.logLevel <= log.ErrorLevel {
		sanitized := sanitizeInput(v...)
		if l.logger != nil {
			l.logger.Println(sanitized...)
		} else {
			golog.Println(sanitized...)
		}
	}
}

func (l *SimpleLogger) Errorf(format string, v ...any) {
	if l.logLevel <= log.ErrorLevel {
		sanitizedFormat := sanitizeFormat(format)
		sanitized := sanitizeInput(v...)
		if l.logger != nil {
			l.logger.Printf(sanitizedFormat, sanitized...)
		} else {
			golog.Printf(sanitizedFormat, sanitized...)
		}
	}
}

func (l *SimpleLogger) Warn(v ...any) {
	if l.logLevel <= log.WarnLevel {
		sanitized := sanitizeInput(v...)
		if l.logger != nil {
			l.logger.Println(sanitized...)
		} else {
			golog.Println(sanitized...)
		}
	}
}

func (l *SimpleLogger) Warnf(format string, v ...any) {
	if l.logLevel <= log.WarnLevel {
		sanitizedFormat := sanitizeFormat(format)
		sanitized := sanitizeInput(v...)
		if l.logger != nil {
			l.logger.Printf(sanitizedFormat, sanitized...)
		} else {
			golog.Printf(sanitizedFormat, sanitized...)
		}
	}
}

func (l *SimpleLogger) Info(v ...any) {
	if l.logLevel <= log.InfoLevel {
		sanitized := sanitizeInput(v...)
		if l.logger != nil {
			l.logger.Println(sanitized...)
		} else {
			golog.Println(sanitized...)
		}
	}
}

func (l *SimpleLogger) Infof(format string, v ...any) {
	if l.logLevel <= log.InfoLevel {
		sanitizedFormat := sanitizeFormat(format)
		sanitized := sanitizeInput(v...)
		if l.logger != nil {
			l.logger.Printf(sanitizedFormat, sanitized...)
		} else {
			golog.Printf(sanitizedFormat, sanitized...)
		}
	}
}

func (l *SimpleLogger) Debug(v ...any) {
	if l.logLevel <= log.AllLevel {
		sanitized := sanitizeInput(v...)
		if l.logger != nil {
			l.logger.Println(sanitized...)
		} else {
			golog.Println(sanitized...)
		}
	}
}

func (l *SimpleLogger) Debugf(format string, v ...any) {
	if l.logLevel <= log.AllLevel {
		sanitizedFormat := sanitizeFormat(format)
		sanitized := sanitizeInput(v...)
		if l.logger != nil {
			l.logger.Printf(sanitizedFormat, sanitized...)
		} else {
			golog.Printf(sanitizedFormat, sanitized...)
		}
	}
}

func (l *SimpleLogger) Trace(v ...any) {
	if l.logLevel <= log.AllLevel {
		sanitized := sanitizeInput(v...)
		if l.logger != nil {
			l.logger.Println(sanitized...)
		} else {
			golog.Println(sanitized...)
		}
	}
}

func (l *SimpleLogger) Tracef(format string, v ...any) {
	if l.logLevel <= log.AllLevel {
		sanitizedFormat := sanitizeFormat(format)
		sanitized := sanitizeInput(v...)
		if l.logger != nil {
			l.logger.Printf(sanitizedFormat, sanitized...)
		} else {
			golog.Printf(sanitizedFormat, sanitized...)
		}
	}
}

func (l *SimpleLogger) SetOutput(w io.Writer) {
	l.logger = golog.New(w, "", 0)
}