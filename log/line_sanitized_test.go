package log

import (
	"strings"
	"testing"
)

// 56 位小写十六进制：命中 [a-f0-9]{40,}，属于"模式命中 ⇒ 只遮那一段"的一类，
// 用它来验清洗确实发生在整行上（而不是被实参类型卡住）。
const lineTestHash = "7e6a4309ddf6e8866679f61ace4f621b0e3455ebac2e831a60f13cd1"

// 8 位短凭据：既不含关键词也够不着 32 位阈值，只有当键名本身是关键词时
// 才会被整行规则吃掉。用它验关键词那一侧的覆盖。
const lineTestShortSecret = "abc123XY"

// 复合实参（map / struct / 切片）过去会绕过整条脱敏链：SanitizeLogInput 的 switch 只认
// string 与 error，其它类型原样穿过格式化。现在清洗排在格式化之后，实参类型不再相关。
//
// 正向对照：把 log.go 的 line/linef 换回 "SanitizeLogInput(v)... + Xxxf"，
// 前两条用例会立刻在"明文出现在日志行里"处变红。
func TestMapArgIsRedactedOnTheWholeLine(t *testing.T) {
	useLevel(t, AllLevel)
	l := &sliceLogger{}
	useLogger(t, l)

	Info("peer:", map[string]string{"peer_id": lineTestHash})
	line := strings.Join(l.take(), "\n")

	if strings.Contains(line, lineTestHash) {
		t.Fatalf("map 实参里的凭据明文进了日志（旧写法正是这样漏掉 tracker 的 user hash）：%q", line)
	}
	if !strings.Contains(line, redactedPlaceholder) {
		t.Fatalf("模式规则没生效，行里连占位符都没有：%q", line)
	}
	// 只该遮命中的那一段：键名与前后文是排障唯一能看见的东西。
	if !strings.Contains(line, "peer_id") || !strings.Contains(line, "peer:") {
		t.Fatalf("整行被一起吞掉了：%q", line)
	}
}

func TestKeywordBearingMapArgRedactsWholeLine(t *testing.T) {
	useLevel(t, AllLevel)
	l := &sliceLogger{}
	useLogger(t, l)

	// websocket 的 Headers 转储就是这一形：值短到没有任何模式能抓住，
	// 但键名 "authorization" 命中关键词 ⇒ 整行打码。
	Debug("[WebSocket] Headers:", map[string]string{"Authorization": "Bearer " + lineTestShortSecret})
	line := strings.Join(l.take(), "\n")

	if strings.Contains(line, lineTestShortSecret) {
		t.Fatalf("短 Bearer token 明文进了日志：%q", line)
	}
	if !strings.Contains(line, redactedPlaceholder) {
		t.Fatalf("关键词命中却没打码：%q", line)
	}
}

// 格式化必须发生在清洗之前，否则数值动词会失配。这条把"%! 不许出现"钉住，
// 也就是钉住"别退回逐个清洗实参再交给 Xxxf"的写法。
func TestLogfKeepsTypedVerbsWorking(t *testing.T) {
	useLevel(t, AllLevel)
	l := &sliceLogger{}
	useLogger(t, l)

	Logf(InfoLevel, "n=%d f=%.2f s=%s cred=%v", 7, 1.5, "ok", lineTestHash)
	line := strings.Join(l.take(), "\n")

	for _, want := range []string{"n=7", "f=1.50", "s=ok"} {
		if !strings.Contains(line, want) {
			t.Fatalf("动词 %q 被打坏了（说明格式化被挪到了清洗之后）：%q", want, line)
		}
	}
	if strings.Contains(line, "%!") {
		t.Fatalf("出现 fmt 的失配标记：%q", line)
	}
	if strings.Contains(line, lineTestHash) {
		t.Fatalf("%%v 拼进文本的凭据没被清洗：%q", line)
	}
}

// 结构体走 %+v 时，字段名会原样进文本；带凭据语义的字段名命中关键词即整行打码。
func TestStructArgWithCredentialFieldIsRedacted(t *testing.T) {
	useLevel(t, AllLevel)
	l := &sliceLogger{}
	useLogger(t, l)

	type session struct {
		Peer     string
		KeyShare string
	}
	Infof("session=%+v", session{Peer: "127.0.0.1:443", KeyShare: lineTestShortSecret})
	line := strings.Join(l.take(), "\n")

	if strings.Contains(line, lineTestShortSecret) {
		t.Fatalf("结构体字段里的明文口令进了日志：%q", line)
	}
	if !strings.Contains(line, redactedPlaceholder) {
		t.Fatalf("字段名 keyshare 该命中关键词却没打码：%q", line)
	}
}
