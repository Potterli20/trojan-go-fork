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
	if l.logLevel > log.FatalLevel {
		os.Exit(1)
	}
	if l.logger != nil {
		l.logger.Fatalln(v...)
	} else {
		golog.Fatalln(v...)
	}
}

func (l *SimpleLogger) Fatalf(format string, v ...any) {
	if l.logLevel > log.FatalLevel {
		os.Exit(1)
	}
	if l.logger != nil {
		l.logger.Fatalf(format, v...)
	} else {
		golog.Fatalf(format, v...)
	}
}

func (l *SimpleLogger) Error(v ...any) {
	if l.logLevel > log.ErrorLevel {
		return
	}
	if l.logger != nil {
		l.logger.Println(v...)
	} else {
		golog.Println(v...)
	}
}

func (l *SimpleLogger) Errorf(format string, v ...any) {
	if l.logLevel > log.ErrorLevel {
		return
	}
	if l.logger != nil {
		l.logger.Printf(format, v...)
	} else {
		golog.Printf(format, v...)
	}
}

func (l *SimpleLogger) Warn(v ...any) {
	if l.logLevel > log.WarnLevel {
		return
	}
	if l.logger != nil {
		l.logger.Println(v...)
	} else {
		golog.Println(v...)
	}
}

func (l *SimpleLogger) Warnf(format string, v ...any) {
	if l.logLevel > log.WarnLevel {
		return
	}
	if l.logger != nil {
		l.logger.Printf(format, v...)
	} else {
		golog.Printf(format, v...)
	}
}

func (l *SimpleLogger) Info(v ...any) {
	if l.logLevel > log.InfoLevel {
		return
	}
	if l.logger != nil {
		l.logger.Println(v...)
	} else {
		golog.Println(v...)
	}
}

func (l *SimpleLogger) Infof(format string, v ...any) {
	if l.logLevel > log.InfoLevel {
		return
	}
	if l.logger != nil {
		l.logger.Printf(format, v...)
	} else {
		golog.Printf(format, v...)
	}
}

func (l *SimpleLogger) Debug(v ...any) {
	if l.logLevel > log.AllLevel {
		return
	}
	if l.logger != nil {
		l.logger.Println(v...)
	} else {
		golog.Println(v...)
	}
}

func (l *SimpleLogger) Debugf(format string, v ...any) {
	if l.logLevel > log.AllLevel {
		return
	}
	if l.logger != nil {
		l.logger.Printf(format, v...)
	} else {
		golog.Printf(format, v...)
	}
}

func (l *SimpleLogger) Trace(v ...any) {
	if l.logLevel > log.AllLevel {
		return
	}
	if l.logger != nil {
		l.logger.Println(v...)
	} else {
		golog.Println(v...)
	}
}

func (l *SimpleLogger) Tracef(format string, v ...any) {
	if l.logLevel > log.AllLevel {
		return
	}
	if l.logger != nil {
		l.logger.Printf(format, v...)
	} else {
		golog.Printf(format, v...)
	}
}

func (l *SimpleLogger) SetOutput(w io.Writer) {
	l.logger = golog.New(w, "", 0)
}