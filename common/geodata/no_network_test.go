package geodata_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// 单元测试不得依赖外网：decode_test.go 原先在 init() 里从 raw.githubusercontent.com
// 下载 23MB 的 geoip.dat，HTTP 503 时 common.Must panic，导致整个包在测试开始前就
// FAIL——CI 上同一提交一成一败，正是网络抖动。本机因有缓存文件一直绿，掩盖了它。
//
// 现在改用内嵌的最小 protobuf 夹具（18B geoip + 30B geosite，解码结果与测试期望
// 逐字节一致），彻底去掉网络与缓存依赖。本门禁钉住：不再有 FetchHTTPContent /
// init() / common.Must 下载路径。
func TestDecodeTestsHaveNoNetworkDependency(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("拿不到本文件名，门禁失效")
	}
	src, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "decode_test.go"))
	if err != nil {
		t.Fatalf("读 decode_test.go: %v", err)
	}
	text := string(src)
	for _, banned := range []string{
		"FetchHTTPContent",
		"raw.githubusercontent.com",
		"common.Must",
		"func init()",
	} {
		if strings.Contains(text, banned) {
			t.Fatalf("decode_test.go 仍含 %q：测试不得在 init/网络下载上依赖外网", banned)
		}
	}
	// 分母自证：内嵌夹具必须在场，否则上面删干净了却没替换物，判据空转。
	if !strings.Contains(text, "geoipFixture") || !strings.Contains(text, "geositeFixture") {
		t.Fatal("找不到内嵌夹具 geoipFixture/geositeFixture：门禁失效")
	}
}
