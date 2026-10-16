package log

import (
	"testing"
)

// TestTraceLevelIsNotDeadCode 钉住 Trace 这一档真的能输出。
//
// 级别常量是「越小越啰嗦」，而最啰嗦的合法配置值是 AllLevel/DebugLevel = 0，
// TraceLevel 却是 -1：门禁按 `level >= currentLogLevel` 比较，于是 Trace 恒被拒，
// log.Trace / log.Tracef 的 7 个调用点（router 的规则命中、freedom 拨号直连、
// tls 握手信息）永远静默——最坑的是 golog 内部其实按 `logLevel == 0` 输出 Trace，
// 也就是说只有外层这一道门禁把它卡死，光看实现代码根本发现不了。
func TestTraceLevelIsNotDeadCode(t *testing.T) {
	t.Run("AllLevel 下 Trace 必须放行", func(t *testing.T) {
		useLevel(t, AllLevel)
		if !ShouldLog(TraceLevel) {
			t.Fatal("ShouldLog(TraceLevel) 在最啰嗦的级别下仍为 false：所有 log.Trace 调用点是死代码")
		}
		l := &sliceLogger{}
		useLogger(t, l)
		Trace("trace-line")
		if got := l.take(); len(got) == 0 {
			t.Fatal("ShouldLog 放行但 logger 没收到：Trace 仍然打不出来")
		}
	})

	t.Run("Info 及以上必须仍然挡住 Trace", func(t *testing.T) {
		for _, lvl := range []LogLevel{InfoLevel, WarnLevel, ErrorLevel, OffLevel} {
			useLevel(t, lvl)
			if ShouldLog(TraceLevel) {
				t.Fatalf("级别 %d 下 Trace 竟然放行", lvl)
			}
			if ShouldLog(DebugLevel) && lvl != DebugLevel {
				t.Fatalf("级别 %d 下 Debug 竟然放行", lvl)
			}
		}
	})
}
