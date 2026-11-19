package quic

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// 选了 brutal 却没给码率，过去只打一行 Warn，连接照常用库默认 BBR 上线：用户以为链路被
// 固定码率整形（brutal 存在的唯一理由），实际没有任何东西生效，运行期也没有观测点。
func TestValidateBrutalParamsRejectsZeroRates(t *testing.T) {
	// brutal：实际速率取 up/down 二者较小值，两者都必须 > 0。
	if err := ValidateBrutalParams(CongestionConfig{Algorithm: "brutal", BrutalUp: 0, BrutalDown: 1000}); err == nil {
		t.Error("brutal 且 brutal-up=0 时必须报错")
	}
	if err := ValidateBrutalParams(CongestionConfig{Algorithm: "brutal", BrutalUp: 1000, BrutalDown: 0}); err == nil {
		t.Error("brutal 且 brutal-down=0 时必须报错")
	}
	// force-brutal：运行期只读 BrutalUp（congestion.go 的 force-brutal 分支），
	// 文档 docs/configuration.md:174 / docs/quic_usage.md:152 / docs/features.md:213
	// 三处明确承诺"只需 brutal-up"。启动校验过去按 brutal 规则连 down 一起要求，
	// 把文档支持的最小合法配置判死。
	if err := ValidateBrutalParams(CongestionConfig{Algorithm: "force-brutal", BrutalUp: 0, BrutalDown: 1000}); err == nil {
		t.Error("force-brutal 且 brutal-up=0 时必须报错（up 是唯一必要参数）")
	}
	if err := ValidateBrutalParams(CongestionConfig{Algorithm: "force-brutal", BrutalUp: 1000, BrutalDown: 0}); err != nil {
		t.Errorf("force-brutal 只需 brutal-up：给了 up 却因 down=0 报错，把文档承诺的配置判死：%v", err)
	}
	for _, algo := range []string{"brutal", "force-brutal"} {
		if err := ValidateBrutalParams(CongestionConfig{Algorithm: algo, BrutalUp: 1, BrutalDown: 1}); err != nil {
			t.Errorf("%s 且两侧码率都为正时不该报错：%v", algo, err)
		}
	}
	// 正向对照：没选 brutal 就不该管这两项（默认 congestion 下 0 是合法值）
	for _, algo := range []string{"", "bbr", "cubic", "reno"} {
		if err := ValidateBrutalParams(CongestionConfig{Algorithm: algo}); err != nil {
			t.Errorf("algorithm=%q 不该被 brutal 的校验拦住：%v", algo, err)
		}
	}
}

// 谓词有牙不等于接线有牙：两侧建栈路径都必须真的调用它。
func TestBrutalValidationIsWiredIntoBothStackBuilders(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("拿不到本文件名，门禁失效")
	}
	dir := filepath.Dir(thisFile)
	for _, file := range []string{"client.go", "server.go"} {
		src, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatalf("读 %s: %v", file, err)
		}
		if !strings.Contains(string(src), "ValidateBrutalParams(") {
			t.Errorf("%s 没有调用 ValidateBrutalParams：brutal 缺参数又会退化成静默 Warn", file)
		}
	}
}
