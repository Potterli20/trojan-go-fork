package simplelog

import (
	"io"
	golog "log"
	"os"

	"gitlab.atcatw.org/atca/community-edition/trojan-go.git/log"
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
		golog.Fatal(v...)
	}
	os.Exit(1)
}

func (l *SimpleLogger) Fatalf(format string, v ...any) {
	if l.logLevel <= log.FatalLevel {
		golog.Fatalf(format, v...)
	}
	os.Exit(1)
}

func (l *SimpleLogger) Error(v ...any) {
	if l.logLevel <= log.ErrorLevel {
		golog.Println(v...)
	}
}

func (l *SimpleLogger) Errorf(format string, v ...any) {
	if l.logLevel <= log.ErrorLevel {
		golog.Printf(format, v...)
	}
}

func (l *SimpleLogger) Warn(v ...any) {
	if l.logLevel <= log.WarnLevel {
		golog.Println(v...)
	}
}

func (l *SimpleLogger) Warnf(format string, v ...any) {
	if l.logLevel <= log.WarnLevel {
		golog.Printf(format, v...)
	}
}

func (l *SimpleLogger) Info(v ...any) {
	if l.logLevel <= log.InfoLevel {
		golog.Println(v...)
	}
}

func (l *SimpleLogger) Infof(format string, v ...any) {
	if l.logLevel <= log.InfoLevel {
		golog.Printf(format, v...)
	}
}

func (l *SimpleLogger) Debug(v ...any) {
	if l.logLevel <= log.AllLevel {
		golog.Println(v...)
	}
}

func (l *SimpleLogger) Debugf(format string, v ...any) {
	if l.logLevel <= log.AllLevel {
		golog.Printf(format, v...)
	}
}

func (l *SimpleLogger) Trace(v ...any) {
	if l.logLevel <= log.AllLevel {
		golog.Println(v...)
	}
}

func (l *SimpleLogger) Tracef(format string, v ...any) {
	if l.logLevel <= log.AllLevel {
		golog.Printf(format, v...)
	}
}

func (l *SimpleLogger) SetOutput(io.Writer) {
	// do nothing
}
