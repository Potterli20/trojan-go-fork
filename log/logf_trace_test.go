package log

import (
	"strings"
	"testing"
)

// Logf 的级别 switch 漏了 TraceLevel：ShouldLog(TraceLevel) 在最啰嗦的
// AllLevel 下放行（Trace 被归一到 Debug 档比较），linef 也完成了格式化与
// 整行脱敏，但 Logf 的 switch 只有 Debug/Info/Warn/Error/Fatal 五个 case，
// TraceLevel(-1) 落不到任何分支，消息被静默丢弃。
// LogConnectionEvent(TraceLevel, ...) 经 Logf 走这条路径，同样丢。
// 顶层 Trace/Tracef 有测试覆盖，这条 bypass 之前没测到。
func TestLogfDoesNotSilentlyDropTraceLevel(t *testing.T) {
	old := GetLogLevel()
	SetLogLevel(AllLevel)
	t.Cleanup(func() { SetLogLevel(old) })

	orig := getLogger()
	defer RegisterLogger(orig)
	cap := &sliceLogger{}
	RegisterLogger(cap)

	Logf(TraceLevel, "TraceLevel via Logf must be emitted")
	if !strings.Contains(strings.Join(cap.take(), "\n"), "TraceLevel via Logf must be emitted") {
		t.Fatal("Logf(TraceLevel, ...) 过了 ShouldLog 门禁却被 switch 静默丢弃")
	}

	LogConnectionEvent(TraceLevel, ConnectionLogFields{
		Module: "TEST", Action: "connect", RemoteAddr: "127.0.0.1:1", TargetAddr: "example.com:443",
	})
	if !strings.Contains(strings.Join(cap.take(), "\n"), "[TEST] connect succeeded") {
		t.Fatal("LogConnectionEvent(TraceLevel, ...) 经 Logf 被静默丢弃")
	}
}
