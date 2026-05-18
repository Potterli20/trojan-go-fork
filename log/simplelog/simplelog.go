package simplelog

import (
	"io"
	golog "log"
	"os"

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

func sanitizeLogInput(v []any) []any {
	return log.SanitizeLogInput(v)
}

func sanitizeString(s string) string {
	return log.SanitizeString(s)
}

func (l *SimpleLogger) SetOutput(w io.Writer) {
	l.logger = golog.New(w, "", 0)
}
