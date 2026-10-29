//go:build linux && (amd64 || 386 || arm || arm64)

package sqlite

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Potterli20/trojan-go-fork/log"
	gormlogger "gorm.io/gorm/logger"
)

// captureLogger 收下递交给日志链的每一行。
//
// 为什么不能改用 log.SetOutput 抓标准输出：这个测试二进制只 import 了 log 包，
// 没有 import log/golog，所以全局 logger 是默认的 EmptyLogger——SetOutput 到哪儿都不写。
// 真实二进制里 component 会注册 golog（目的地正是 os.Stdout），那才是泄漏发生的现场。
// 注册一个替身既还原了"有没有人收"，又正好验到点上：清洗发生在 log 包入口，
// 递交给 logger 的字符串已经该是干净的。
type captureLogger struct {
	mu    sync.Mutex
	lines []string
}

func (c *captureLogger) record(v ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, fmt.Sprintln(v...))
}

func (c *captureLogger) take() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := strings.Join(c.lines, "")
	c.lines = nil
	return out
}

func (c *captureLogger) Fatal(v ...any)            { c.record(v...) }
func (c *captureLogger) Fatalf(f string, v ...any) { c.record(fmt.Sprintf(f, v...)) }
func (c *captureLogger) Error(v ...any)            { c.record(v...) }
func (c *captureLogger) Errorf(f string, v ...any) { c.record(fmt.Sprintf(f, v...)) }
func (c *captureLogger) Warn(v ...any)             { c.record(v...) }
func (c *captureLogger) Warnf(f string, v ...any)  { c.record(fmt.Sprintf(f, v...)) }
func (c *captureLogger) Info(v ...any)             { c.record(v...) }
func (c *captureLogger) Infof(f string, v ...any)  { c.record(fmt.Sprintf(f, v...)) }
func (c *captureLogger) Debug(v ...any)            { c.record(v...) }
func (c *captureLogger) Debugf(f string, v ...any) { c.record(fmt.Sprintf(f, v...)) }
func (c *captureLogger) Trace(v ...any)            { c.record(v...) }
func (c *captureLogger) Tracef(f string, v ...any) { c.record(fmt.Sprintf(f, v...)) }
func (c *captureLogger) SetLogLevel(log.LogLevel)  {}
func (c *captureLogger) SetOutput(io.Writer)       {}

var _ log.Logger = (*captureLogger)(nil)

// useCapture 把替身注册成全局 logger，用完恢复本二进制的初始状态
// （EmptyLogger + 原级别）。RegisterLogger 会把包内级别镜像同步给新 logger，
// 所以先设级别、再注册。
func useCapture(t *testing.T) *captureLogger {
	t.Helper()
	c := &captureLogger{}
	previousLevel := log.GetLogLevel()
	t.Cleanup(func() {
		log.SetLogLevel(previousLevel)
		log.RegisterLogger(&log.EmptyLogger{})
	})
	log.SetLogLevel(log.AllLevel)
	log.RegisterLogger(c)
	return c
}

// gorm 的默认 logger 直接写 os.Stdout，而它 Warn 级输出的是**变量已经内插进去**的完整
// SQL 文本（gorm logger/logger.go:76 的 Default，以及回调递给 Trace 的那个 fc()）。
// 本后端的 password 列存的是 u.GetKeyShare()，而 memory 那边那个 getter 返回的是
// **原始明文口令**——于是一条 >200ms 的写库、或库锁冲突重试，就把明文口令按 SQL 原文
// 打到标准输出，完全绕过 log 包的清洗链。CI 日志里就能看到它的形态：
// [0.056ms] [rows:0] SELECT * FROM `users` WHERE hash = "hash-a" ...（值已内插）。
//
// 正向对照：把 NewSqlitePersistencer 里的 &gorm.Config{Logger: newGormLogger()}
// 退回 &gorm.Config{}，第二条用例立刻变红（第一条是结构性断言，同样变红）。
func TestGormLoggerIsNotTheStdoutDefault(t *testing.T) {
	p := newTestPersistencer(t)
	if p.db.Logger == gormlogger.Default {
		t.Fatal("落回 gorm 的 stdout 默认 logger：内插了明文口令的 SQL 会绕过清洗链直接写标准输出")
	}
}

func TestGormSlowQueryGoesThroughTheSanitizer(t *testing.T) {
	p := newTestPersistencer(t)
	sunk := useCapture(t)

	const plaintext = "sup3rsecretvalue"
	sqlText := "INSERT INTO `users` (`hash`,`password`) VALUES (\"aaaa\",\"" + plaintext + "\")"
	// begin 拨回 1s，越过 200ms 的 SlowThreshold，走慢查询那条分支
	p.db.Logger.Trace(context.Background(), time.Now().Add(-time.Second),
		func() (string, int64) { return sqlText, 1 }, nil)

	got := sunk.take()
	if strings.Contains(got, plaintext) {
		t.Fatalf("明文口令经 gorm 漏了出来：%q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("这一行含 password 列名，本该被关键词整行打码，却没有占位符：%q", got)
	}
}

// record not found 是本仓的正常控制流（LoadUser 按 err 分支处理），不该每次都往
// 日志里刷一行带内插值的 SQL。
func TestGormIgnoresRecordNotFoundNoise(t *testing.T) {
	p := newTestPersistencer(t)
	sunk := useCapture(t)

	if err := p.db.Where("hash = ?", "no-such-user").First(&User{}).Error; err == nil {
		t.Fatal("查不到行必须返回错误")
	}
	if got := sunk.take(); strings.Contains(got, "record not found") {
		t.Fatalf("record not found 仍在刷日志（冷启动装载每个用户都会来一次）：%q", got)
	}
}
