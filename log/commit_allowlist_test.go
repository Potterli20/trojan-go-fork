package log

import (
	"strings"
	"testing"
)

// git commit SHA 正好 40 位十六进制，落在 [a-f0-9]{40,} 的入口长度上，形状和 Trojan 的
// 线上认证物（SHA224，56 位）无法区分。所以下面这几条一起构成这道白名单的边界：
// 带标识词的 40 位放过，其余一律照遮。
const (
	allowCommitSHA = "0123456789abcdef0123456789abcdef01234567" // 40 位
	allowUserHash  = "7e6a4309ddf6e8866679f61ace4f621b0e3455ebac2e831a60f13cd1"
)

func captureOne(t *testing.T, emit func()) string {
	t.Helper()
	useLevel(t, AllLevel)
	l := &sliceLogger{}
	useLogger(t, l)
	emit()
	return strings.Join(l.take(), "\n")
}

func TestCommitLabelledSHASurvives(t *testing.T) {
	line := captureOne(t, func() { Info("Commit:", allowCommitSHA) })
	if !strings.Contains(line, allowCommitSHA) {
		t.Fatalf("启动横幅的 git SHA 又被吃掉了，运维将无法确认跑的是哪个提交：%q", line)
	}
	if strings.Contains(line, redactedPlaceholder) {
		t.Fatalf("这一行本该整段保留：%q", line)
	}
	// 哨兵绝不残留
	if strings.ContainsRune(line, safeMarkByte) {
		t.Fatalf("脱敏哨兵漏进了输出：%q", line)
	}
	if !strings.Contains(line, "Commit: "+allowCommitSHA) {
		t.Fatalf("分隔空白被改动（横幅格式会被下游解析依赖）：%q", line)
	}
}

// 没有标识词上下文的 40 位 hex 仍然按凭据处理：白名单认的是"commit:"这种语境，不是长度。
func TestBare40HexStillRedacted(t *testing.T) {
	line := captureOne(t, func() { Info("peer value is:", allowCommitSHA) })
	if strings.Contains(line, allowCommitSHA) {
		t.Fatalf("裸 40 位十六进制没被遮：%q", line)
	}
}

// 56 位 SHA224 即便跟在 "Commit:" 后面也照遮——白名单只放过整整 40 位。
func TestSHA224AfterCommitLabelStillRedacted(t *testing.T) {
	line := captureOne(t, func() { Info("Commit:", allowUserHash) })
	if strings.Contains(line, allowUserHash) {
		t.Fatalf("56 位用户 hash 借了 commit 标签就漏出去：%q", line)
	}
	if !strings.Contains(line, redactedPlaceholder) {
		t.Fatalf("该命中模式规则却没有占位符：%q", line)
	}
}

// 对端把哨字节连同凭据一起喂进来时，必须既留不下哨兵、也逃不掉脱敏：
// 顺序只能是"先清输入里的哨字节，再制造哨兵"。
//
// 变异对照（实测）：去掉那一行 ReplaceAll，输入自带的 \x01 会一路活到输出——恢复阶段
// 拿 saved 去替换标记时，攻击者的文本就成了替换目标，整行的可预测性都没了。
// 白名单本身还有一条已知边界，写在这里而不是藏起来：对端若能控制一段被打印的字节，
// 它就可以自己拼一句 "commit: <40位>" 让那 40 位免于打码。免掉的永远是它自己的字节，
// 不是我们的凭据，所以这道上下文白名单仍然是净收益。
func TestSentinelBytesCannotSurviveOrBypassRedaction(t *testing.T) {
	planted := string(safeMarkByte) + "0" + string(safeMarkByte) +
		allowUserHash + string(safeMarkByte) + "0" + string(safeMarkByte)
	line := captureOne(t, func() { Info(planted) })
	if strings.Contains(line, allowUserHash) {
		t.Fatalf("自带哨兵绕过了脱敏：%q", line)
	}
	if strings.ContainsRune(line, safeMarkByte) {
		t.Fatalf("输入的哨字节没被清掉：%q", line)
	}

	// 更刁的一形：把 commit 标签和哨兵拼在一起，凭据本身仍然必须被遮。
	line2 := captureOne(t, func() { Info("commit:", string(safeMarkByte)+allowUserHash) })
	if strings.Contains(line2, allowUserHash) {
		t.Fatalf("哨兵拼在 hash 前面就逃过规则：%q", line2)
	}
}

// 大小写与 : / = 两种分隔都要认：横幅是 "Commit:"，而别的模块可能写成 "rev = "。
func TestAllowlistAcceptsLabelVariants(t *testing.T) {
	for _, s := range []string{
		"commit: " + allowCommitSHA,
		"REV = " + allowCommitSHA,
		"revision:" + allowCommitSHA,
	} {
		line := captureOne(t, func() { Info(s) })
		if !strings.Contains(line, allowCommitSHA) {
			t.Fatalf("写法 %q 没被白名单认出来：%q", s, line)
		}
	}
}
