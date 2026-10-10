package quic

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 本包的两个窗口字段（initial-stream-window / initial-conn-window）曾经"声明了但没人读"，
// 于是它们的默认值 65535 一直没人察觉；直到某次"让配置生效"的改动把它们接进 quic.Config，
// 才等于顺手把所有人的默认流控窗口缩到 1/8。同一族的还有一个 enabled：声明、默认为 false、
// 全仓零消费——用户写下 quic.enabled: true 时得到的是"什么都没发生"。
//
// 所以这里钉住一条契约：QUICConfig 的每一枚字段都必须真的被读到。
// 新增字段而忘了接线，会在这里变红；删掉字段则要同步改下面的期望清单。
func TestEveryQuicConfigFieldIsConsumed(t *testing.T) {
	src, err := os.ReadFile("config.go")
	if err != nil {
		t.Fatalf("读取 config.go 失败: %v", err)
	}
	fields := structFields(string(src), "QUICConfig")
	// 下限自证：解析真的扫到了字段，而不是正则空转
	want := []string{
		"MaxIdleTimeout", "MaxIncomingStreams", "InitialStreamWindow", "InitialConnWindow",
		"ALPN", "Insecure", "Congestion", "BrutalUp", "BrutalDown",
	}
	if len(fields) != len(want) {
		t.Fatalf("QUICConfig 字段清单变成 %v（期望 %d 枚 %v）：请同步更新本用例，"+
			"并确认新字段真的被读到", fields, len(want), want)
	}

	consumers, err := packageSourceExcept("config.go")
	if err != nil {
		t.Fatalf("读取包内其它源文件失败: %v", err)
	}
	if !strings.Contains(consumers, "cfg.QUIC.") {
		t.Fatal(`读到的包源码里连 "cfg.QUIC." 都没有，本用例的判据是无意义的`)
	}
	for _, f := range fields {
		re := regexp.MustCompile(`\bQUIC\.` + regexp.QuoteMeta(f) + `\b`)
		if !re.MatchString(consumers) {
			t.Errorf("字段 %s 声明了却没有任何代码读取它：配置写了也没用，"+
				"要么接线、要么删掉", f)
		}
	}
}

// structFields 取出某个 struct 的字段名列表（按声明顺序）。
func structFields(source, typeName string) []string {
	marker := "type " + typeName + " struct {"
	i := strings.Index(source, marker)
	if i < 0 {
		return nil
	}
	body := source[i+len(marker):]
	end := strings.Index(body, "\n}")
	if end < 0 {
		return nil
	}
	lineRe := regexp.MustCompile(`^\t([A-Z]\w*)\s+\S+\s+` + "`")
	var out []string
	for _, line := range strings.Split(body[:end], "\n") {
		if m := lineRe.FindStringSubmatch(line); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

// packageSourceExcept 拼接包目录里所有非测试源文件的内容，跳过排除项。
func packageSourceExcept(exclude ...string) (string, error) {
	entries, err := os.ReadDir(".")
	if err != nil {
		return "", err
	}
	skip := map[string]bool{}
	for _, e := range exclude {
		skip[e] = true
	}
	var sb strings.Builder
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || skip[name] || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			return "", err
		}
		sb.Write(data)
		sb.WriteByte('\n')
	}
	return sb.String(), nil
}
