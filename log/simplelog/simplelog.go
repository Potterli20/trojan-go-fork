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
		sanitized := log.SanitizeLogInput(v)
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
		sanitizedFormat := log.SanitizeString(format)
		sanitized := log.SanitizeLogInput(v)
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
		sanitized := log.SanitizeLogInput(v)
		if l.logger != nil {
			l.logger.Println(sanitized...)
		} else {
			golog.Println(sanitized...)
		}
	}
}

func (l *SimpleLogger) Errorf(format string, v ...any) {
	if l.logLevel <= log.ErrorLevel {
		sanitizedFormat := log.SanitizeString(format)
		sanitized := log.SanitizeLogInput(v)
		if l.logger != nil {
			l.logger.Printf(sanitizedFormat, sanitized...)
		} else {
			golog.Printf(sanitizedFormat, sanitized...)
		}
	}
}

func (l *SimpleLogger) Warn(v ...any) {
	if l.logLevel <= log.WarnLevel {
		sanitized := log.SanitizeLogInput(v)
		if l.logger != nil {
			l.logger.Println(sanitized...)
		} else {
			golog.Println(sanitized...)
		}
	}
}

func (l *SimpleLogger) Warnf(format string, v ...any) {
	if l.logLevel <= log.WarnLevel {
		sanitizedFormat := log.SanitizeString(format)
		sanitized := log.SanitizeLogInput(v)
		if l.logger != nil {
			l.logger.Printf(sanitizedFormat, sanitized...)
		} else {
			golog.Printf(sanitizedFormat, sanitized...)
		}
	}
}

func (l *SimpleLogger) Info(v ...any) {
	if l.logLevel <= log.InfoLevel {
		sanitized := log.SanitizeLogInput(v)
		if l.logger != nil {
			l.logger.Println(sanitized...)
		} else {
			golog.Println(sanitized...)
		}
	}
}

func (l *SimpleLogger) Infof(format string, v ...any) {
	if l.logLevel <= log.InfoLevel {
		sanitizedFormat := log.SanitizeString(format)
		sanitized := log.SanitizeLogInput(v)
		if l.logger != nil {
			l.logger.Printf(sanitizedFormat, sanitized...)
		} else {
			golog.Printf(sanitizedFormat, sanitized...)
		}
	}
}

func (l *SimpleLogger) Debug(v ...any) {
	if l.logLevel <= log.AllLevel {
		sanitized := log.SanitizeLogInput(v)
		if l.logger != nil {
			l.logger.Println(sanitized...)
		} else {
			golog.Println(sanitized...)
		}
	}
}

func (l *SimpleLogger) Debugf(format string, v ...any) {
	if l.logLevel <= log.AllLevel {
		sanitizedFormat := log.SanitizeString(format)
		sanitized := log.SanitizeLogInput(v)
		if l.logger != nil {
			l.logger.Printf(sanitizedFormat, sanitized...)
		} else {
			golog.Printf(sanitizedFormat, sanitized...)
		}
	}
}

func (l *SimpleLogger) Trace(v ...any) {
	if l.logLevel <= log.AllLevel {
		sanitized := log.SanitizeLogInput(v)
		if l.logger != nil {
			l.logger.Println(sanitized...)
		} else {
			golog.Println(sanitized...)
		}
	}
}

func (l *SimpleLogger) Tracef(format string, v ...any) {
	if l.logLevel <= log.AllLevel {
		sanitizedFormat := log.SanitizeString(format)
		sanitized := log.SanitizeLogInput(v)
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
