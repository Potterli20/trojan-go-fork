package golog

import (
	"os"
	"strings"
	"testing"

	"github.com/Potterli20/trojan-go-fork/log"
)

// newDevNullLogger 拿 /dev/null 当输出：New 会读 Fd() 判断是不是终端，
// 所以不能塞一个裸 io.Discard。
func newDevNullLogger(t *testing.T) *Logger {
	t.Helper()
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { f.Close() }) //gosec:disable -- 错误忽略：测试清理
	return New(f)
}

// TestOutputDoesNotRememberPeakLineSize 钉住行缓冲的高水位驻留。
//
// Logger 只有一条行缓冲，每行开头 Reset；而 buffer.Buffer.Reset 是 b[:0]，
// 只收长度不收容量。于是出现过一条超长日志行之后，容量就永久停在那个高度——
// "单条最长行"直接变成进程常驻内存。日志里带上对端可控的长内容（长 URL、长 SNI、
// 握手失败的原始字节）是做得到的，所以这个峰值不该被记住。
func TestOutputDoesNotRememberPeakLineSize(t *testing.T) {
	l := newDevNullLogger(t)
	l.SetLogLevel(log.DebugLevel)

	// 用 "x " 交替而不是整串字母数字：后者会命中脱敏里的"长 secret"模式，
	// 整段被换成 [REDACTED]，压根到不了缓冲区。
	huge := strings.Repeat("x ", 32*1024)
	l.Debug(huge)
	if cap(l.buf.Buffer) <= logBufferKeepLimit {
		t.Fatalf("前置条件不成立：写了 64KB 的行之后容量只有 %d", cap(l.buf.Buffer))
	}

	// 之后的小行不该继续背着那个峰值容量
	l.Debug("tiny line")
	if got := cap(l.buf.Buffer); got > logBufferKeepLimit {
		t.Fatalf("超长行之后容量仍是 %d（保留上限 %d）：峰值被永久驻留", got, logBufferKeepLimit)
	}
	if got := cap(l.buf.Buffer); got < logBufferResetSize {
		t.Fatalf("回落后容量 %d 小于回落尺寸 %d：每条正常日志都要重新扩容", got, logBufferResetSize)
	}
}

// TestOutputKeepsNormalSizedBuffer 保证回落不是"每行都重建缓冲"。
func TestOutputKeepsNormalSizedBuffer(t *testing.T) {
	l := newDevNullLogger(t)
	l.SetLogLevel(log.DebugLevel)

	for range 50 {
		l.Debug("a perfectly ordinary log line with some payload")
	}
	if got := cap(l.buf.Buffer); got > logBufferKeepLimit {
		t.Fatalf("50 条正常长度日志后容量 %d 越过了保留上限", got)
	}
	if got := cap(l.buf.Buffer); got == 0 {
		t.Fatal("正常路径上缓冲被反复清成零容量")
	}
}
