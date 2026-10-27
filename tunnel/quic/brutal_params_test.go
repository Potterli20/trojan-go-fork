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
	for _, algo := range []string{"brutal", "force-brutal"} {
		if err := ValidateBrutalParams(CongestionConfig{Algorithm: algo, BrutalUp: 0, BrutalDown: 1000}); err == nil {
			t.Errorf("%s 且 brutal-up=0 时必须报错", algo)
		}
		if err := ValidateBrutalParams(CongestionConfig{Algorithm: algo, BrutalUp: 1000, BrutalDown: 0}); err == nil {
			t.Errorf("%s 且 brutal-down=0 时必须报错", algo)
		}
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
