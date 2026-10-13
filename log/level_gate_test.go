package log

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
)

// sliceLogger 把收到的每一次调用原样记下来，用来区分「级别门禁短路」和
// 「logger 自己按级别丢弃」——前者根本不该调用到 logger。
type sliceLogger struct {
	mu      sync.Mutex
	records []string
	level   LogLevel
}

func (l *sliceLogger) record(prefix string, v ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	parts := make([]string, 0, len(v)+1)
	parts = append(parts, prefix)
	for _, item := range v {
		parts = append(parts, fmt.Sprint(item))
	}
	l.records = append(l.records, strings.Join(parts, " "))
}

func (l *sliceLogger) take() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.records
	l.records = nil
	return out
}

func (l *sliceLogger) Fatal(v ...any)             { l.record("fatal", v...) }
func (l *sliceLogger) Fatalf(f string, v ...any)  { l.record("fatal", fmt.Sprintf(f, v...)) }
func (l *sliceLogger) Error(v ...any)             { l.record("error", v...) }
func (l *sliceLogger) Errorf(f string, v ...any)  { l.record("error", fmt.Sprintf(f, v...)) }
func (l *sliceLogger) Warn(v ...any)              { l.record("warn", v...) }
func (l *sliceLogger) Warnf(f string, v ...any)   { l.record("warn", fmt.Sprintf(f, v...)) }
func (l *sliceLogger) Info(v ...any)              { l.record("info", v...) }
func (l *sliceLogger) Infof(f string, v ...any)   { l.record("info", fmt.Sprintf(f, v...)) }
func (l *sliceLogger) Debug(v ...any)             { l.record("debug", v...) }
func (l *sliceLogger) Debugf(f string, v ...any)  { l.record("debug", fmt.Sprintf(f, v...)) }
func (l *sliceLogger) Trace(v ...any)             { l.record("trace", v...) }
func (l *sliceLogger) Tracef(f string, v ...any)  { l.record("trace", fmt.Sprintf(f, v...)) }
func (l *sliceLogger) SetLogLevel(level LogLevel) { l.level = level }
func (l *sliceLogger) SetOutput(w io.Writer)      {}

var _ Logger = (*sliceLogger)(nil)

// useLogger 临时替换全局 logger，测试结束恢复。
func useLogger(t *testing.T, l Logger) {
	t.Helper()
	loggerMu.Lock()
	previous := logger
	loggerMu.Unlock()
	t.Cleanup(func() { RegisterLogger(previous) })
	RegisterLogger(l)
}

func useLevel(t *testing.T, level LogLevel) {
	t.Helper()
	previous := GetLogLevel()
	t.Cleanup(func() { SetLogLevelInternal(previous) })
	SetLogLevelInternal(level)
}

// TestSetLogLevelMovesTheGate 盯 SetLogLevel 与 ShouldLog 的关系。
//
// 级别其实存了两份：各 logger 实现自己一份，包内镜像 currentLogLevel 一份，
// 而 Logf / LogConnectionEvent 的门禁读的是镜像。旧实现里只有
// SetLogLevelInternal 会写镜像，并且只有测试在调它 —— 于是配置文件把级别设成
// Info 之后，镜像仍停在 AllLevel，门禁恒为真，被丢弃的 Debug 消息照旧跑完
// 一整轮脱敏（6 条正则 + 3 次 ReplaceAll + html.EscapeString）和一把 RWMutex 读锁。
func TestSetLogLevelMovesTheGate(t *testing.T) {
	useLevel(t, AllLevel)
	rec := &sliceLogger{}
	useLogger(t, rec)

	SetLogLevel(InfoLevel)
	if ShouldLog(DebugLevel) {
		t.Fatal("SetLogLevel 没有更新镜像级别：ShouldLog 的门禁恒为真")
	}
	if !ShouldLog(InfoLevel) || !ShouldLog(ErrorLevel) {
		t.Fatal("门禁把仍应输出的级别一起挡掉了")
	}

	SetLogLevel(AllLevel)
	if !ShouldLog(DebugLevel) {
		t.Fatal("放开到 AllLevel 后 Debug 仍被短路")
	}
}

// TestDisabledLevelNeverTouchesLogger 确认短路发生在调用 logger 之前，
// 而不是靠 logger 自己按级别丢弃（后者依然要为脱敏付钱）。
func TestDisabledLevelNeverTouchesLogger(t *testing.T) {
	useLevel(t, OffLevel)
	rec := &sliceLogger{}
	useLogger(t, rec)

	Warn("a warning")
	Info("some info")
	Debug("some debug")
	Trace("some trace")
	Error("some error")
	Errorf("formatted %s", "error")

	if got := rec.take(); len(got) != 0 {
		t.Fatalf("OffLevel 下仍有 %d 条消息走到了 logger：%q", len(got), got)
	}
}

// TestEnabledLevelStillSanitizes 是反向保险：短路不能把脱敏一起短路掉。
// 凭据脱敏是 AGENTS.md 里明确的行为约束，级别放开时必须照旧生效。
func TestEnabledLevelStillSanitizes(t *testing.T) {
	useLevel(t, AllLevel)
	rec := &sliceLogger{}
	useLogger(t, rec)

	Debug("password=SuperSecretValue123")
	Warn("the api-key: abcdefghijklmnopqrstuvwxyz0123456789")

	got := rec.take()
	if len(got) != 2 {
		t.Fatalf("期望 2 条，实际 %d 条：%q", len(got), got)
	}
	for _, line := range got {
		if strings.Contains(line, "SuperSecretValue123") || strings.Contains(line, "abcdefghijklmnopqrstuvwxyz0123456789") {
			t.Fatalf("凭据没被遮住：%q", line)
		}
		if !strings.Contains(line, redactedPlaceholder) {
			t.Fatalf("脱敏没生效：%q", line)
		}
	}
}

// TestFatalIsNeverGated 保证退出通道不被短路：log.Fatal 负责结束进程，
// 级别设成 OffLevel 也不能让它变成空操作，否则错误会被静默吞掉且进程继续跑。
func TestFatalIsNeverGated(t *testing.T) {
	useLevel(t, OffLevel)
	rec := &sliceLogger{}
	useLogger(t, rec)

	Fatal("must be delivered")
	Fatalf("must be %s", "delivered")

	if got := rec.take(); len(got) != 2 {
		t.Fatalf("Fatal 被级别短路了，实际收到 %d 条：%q", len(got), got)
	}
}

// TestDisabledDebugAllocatesNothingMuch 用分配数钉住「先判级别再清洗」这件事。
// 变参调用本身要分配一个 []any，所以门槛是 1；旧实现在这之上还要为
// SanitizeString/ObfuscateSensitiveData/EscapeString 付出十几二十次分配。
func TestDisabledDebugAllocatesNothingMuch(t *testing.T) {
	useLevel(t, InfoLevel)
	rec := &sliceLogger{}
	useLogger(t, rec)

	message := "rewind buffer exceeded hard limit for https://example.com/some/long/path"
	allocs := testing.AllocsPerRun(200, func() {
		Debug(message)
	})
	if allocs > 1 {
		t.Fatalf("被短路的 Debug 仍然分配了 %g 次/调用（脱敏没被跳过）", allocs)
	}
	if got := rec.take(); len(got) != 0 {
		t.Fatalf("InfoLevel 下 Debug 仍被投递：%q", got)
	}
}
