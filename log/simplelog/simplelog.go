package simplelog

import (
	"io"
	golog "log"
	"os"
	"sync/atomic"

	"github.com/Potterli20/trojan-go-fork/log"
)

func init() {
	log.RegisterLogger(&SimpleLogger{})
}

type SimpleLogger struct {
	// logLevel / logger 会被并发的代理 goroutine 读取，而 SetLogLevel / SetOutput
	// 可能在运行期被调用（API 调级、日志文件重挂）。必须与 golog.Logger 一样
	// 用原子类型读写，否则 race detector 会报数据竞争。
	logLevel atomic.Int32
	logger   atomic.Pointer[golog.Logger]
}

func (l *SimpleLogger) SetLogLevel(level log.LogLevel) {
	l.logLevel.Store(int32(level)) //gosec:disable -- LogLevel 是小枚举值（0-5），转换安全
}

func (l *SimpleLogger) getLogLevel() log.LogLevel {
	return log.LogLevel(l.logLevel.Load()) //gosec:disable -- int32 来自 LogLevel 枚举，转换安全
}

func (l *SimpleLogger) getLogger() *golog.Logger {
	return l.logger.Load()
}

func (l *SimpleLogger) Fatal(v ...any) {
	if l.getLogLevel() > log.FatalLevel {
		os.Exit(1)
	}
	sanitized := log.SanitizeLogInput(v)
	if logger := l.getLogger(); logger != nil {
		logger.Fatalln(sanitized...)
	} else {
		golog.Fatalln(sanitized...)
	}
}

func (l *SimpleLogger) Fatalf(format string, v ...any) {
	if l.getLogLevel() > log.FatalLevel {
		os.Exit(1)
	}
	sanitizedFormat := log.SanitizeString(format)
	sanitized := log.SanitizeLogInput(v)
	if logger := l.getLogger(); logger != nil {
		logger.Fatalf(sanitizedFormat, sanitized...)
	} else {
		golog.Fatalf(sanitizedFormat, sanitized...)
	}
}

func (l *SimpleLogger) Error(v ...any) {
	if l.getLogLevel() > log.ErrorLevel {
		return
	}
	sanitized := log.SanitizeLogInput(v)
	if logger := l.getLogger(); logger != nil {
		logger.Println(sanitized...)
	} else {
		golog.Println(sanitized...)
	}
}

func (l *SimpleLogger) Errorf(format string, v ...any) {
	if l.getLogLevel() > log.ErrorLevel {
		return
	}
	sanitizedFormat := log.SanitizeString(format)
	sanitized := log.SanitizeLogInput(v)
	if logger := l.getLogger(); logger != nil {
		logger.Printf(sanitizedFormat, sanitized...)
	} else {
		golog.Printf(sanitizedFormat, sanitized...)
	}
}

func (l *SimpleLogger) Warn(v ...any) {
	if l.getLogLevel() > log.WarnLevel {
		return
	}
	sanitized := log.SanitizeLogInput(v)
	if logger := l.getLogger(); logger != nil {
		logger.Println(sanitized...)
	} else {
		golog.Println(sanitized...)
	}
}

func (l *SimpleLogger) Warnf(format string, v ...any) {
	if l.getLogLevel() > log.WarnLevel {
		return
	}
	sanitizedFormat := log.SanitizeString(format)
	sanitized := log.SanitizeLogInput(v)
	if logger := l.getLogger(); logger != nil {
		logger.Printf(sanitizedFormat, sanitized...)
	} else {
		golog.Printf(sanitizedFormat, sanitized...)
	}
}

func (l *SimpleLogger) Info(v ...any) {
	if l.getLogLevel() > log.InfoLevel {
		return
	}
	sanitized := log.SanitizeLogInput(v)
	if logger := l.getLogger(); logger != nil {
		logger.Println(sanitized...)
	} else {
		golog.Println(sanitized...)
	}
}

func (l *SimpleLogger) Infof(format string, v ...any) {
	if l.getLogLevel() > log.InfoLevel {
		return
	}
	sanitizedFormat := log.SanitizeString(format)
	sanitized := log.SanitizeLogInput(v)
	if logger := l.getLogger(); logger != nil {
		logger.Printf(sanitizedFormat, sanitized...)
	} else {
		golog.Printf(sanitizedFormat, sanitized...)
	}
}

func (l *SimpleLogger) Debug(v ...any) {
	if l.getLogLevel() > log.AllLevel {
		return
	}
	sanitized := log.SanitizeLogInput(v)
	// 输入已经过 SanitizeLogInput 清洗，CodeQL 无法识别自定义 sanitizer
	// codeql[go/log-injection] ignore
	if logger := l.getLogger(); logger != nil {
		logger.Println(sanitized...)
	} else {
		// codeql[go/log-injection] ignore
		golog.Println(sanitized...)
	}
}

func (l *SimpleLogger) Debugf(format string, v ...any) {
	if l.getLogLevel() > log.AllLevel {
		return
	}
	sanitizedFormat := log.SanitizeString(format)
	sanitized := log.SanitizeLogInput(v)
	if logger := l.getLogger(); logger != nil {
		logger.Printf(sanitizedFormat, sanitized...)
	} else {
		golog.Printf(sanitizedFormat, sanitized...)
	}
}

func (l *SimpleLogger) Trace(v ...any) {
	if l.getLogLevel() > log.AllLevel {
		return
	}
	sanitized := log.SanitizeLogInput(v)
	if logger := l.getLogger(); logger != nil {
		logger.Println(sanitized...)
	} else {
		golog.Println(sanitized...)
	}
}

func (l *SimpleLogger) Tracef(format string, v ...any) {
	if l.getLogLevel() > log.AllLevel {
		return
	}
	sanitizedFormat := log.SanitizeString(format)
	sanitized := log.SanitizeLogInput(v)
	if logger := l.getLogger(); logger != nil {
		logger.Printf(sanitizedFormat, sanitized...)
	} else {
		golog.Printf(sanitizedFormat, sanitized...)
	}
}

func (l *SimpleLogger) SetOutput(w io.Writer) {
	l.logger.Store(golog.New(w, "", 0))
}
