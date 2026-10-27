package log

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// 一行日志必须真的只有一行。全仓主流写法是 log.Error(common.NewError(...).Base(err))，
// 而 SanitizeLogInput 过去只重写 string 实参——error 原样穿过 fmt.Sprintln，落在
// golog 的行缓冲上（Output 只 Append，不再二次清洗）。对端可控字节里的 CR/LF
// 因此能伪造整行日志：域名/SNI/目的地是 metadata.go 里长度前缀的任意字节，
// 而 freedom 的拨号错误会把 addr.String() 拼进错误文案。
func TestSanitizeLogInputFlattensNewlinesInsideErrors(t *testing.T) {
	out := SanitizeLogInput([]any{
		"拨号失败",
		errors.New("peer sent a\n2026/10/27 12:00:00 FORGED ERROR line\r\nsecond forged"),
	})
	line := fmt.Sprintln(out...)
	if n := strings.Count(line, "\n"); n != 1 {
		t.Fatalf("一行日志里有 %d 个换行，对端可借此伪造任意日志行：%q", n, line)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Fatalf("清洗后仍应以单个换行收尾：%q", line)
	}
	// 内容只被压平、不该整体丢失，否则排障信息一起没了。
	if !strings.Contains(line, "FORGED ERROR") {
		t.Fatalf("压平不应吞掉诊断内容：%q", line)
	}
}

// 同一条链还得吃到凭据脱敏：error 文案里带凭据时不能原样落盘。
func TestSanitizeLogInputObfuscatesCredentialsInsideErrors(t *testing.T) {
	const secret = "z4Kx9Qm2Vb7Tn8Rj5Ld3Pg1Wh6Ys0Cu"
	out := SanitizeLogInput([]any{errors.New("handshake failed with token " + secret)})
	line := fmt.Sprintln(out...)
	if strings.Contains(line, secret) {
		t.Fatalf("error 文案里的凭据没被脱敏：%q", line)
	}
}

// 正向对照：string 分支的行为不能被 switch 改写弄丢。
func TestSanitizeLogInputStillCleansPlainStrings(t *testing.T) {
	out := SanitizeLogInput([]any{"a\nb", error(nil)})
	line := fmt.Sprintln(out...)
	if strings.Count(line, "\n") != 1 {
		t.Fatalf("string 分支失效：%q", line)
	}
}
