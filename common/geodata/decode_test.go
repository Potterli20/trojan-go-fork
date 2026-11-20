package geodata_test

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Potterli20/trojan-go-fork/common/geodata"
)

// 最小 protobuf 夹具：一条 code="TEST" 的 GeoIP（CIDR 127.0.0.0/8）与一条
// code="TEST" 的 GeoSite（域名 test.example.com）。这两个字节串由 v2ray 的
// GeoIPList/GeoSiteList protobuf 生成，Decode("test") 的结果与原先从 GitHub
// 下载的 23MB 官方数据里的 "test" 条目逐字节一致（大小写不敏感）。
//
// 测试彻底不再依赖外网：旧实现在 init 里从远端下载 23MB 的 geoip.dat，
// HTTP 503 时直接 panic，整个包在测试开始前就 FAIL——CI 上同一提交一成一败。
var (
	geoipFixture = []byte{
		0x0a, 0x10, 0x0a, 0x04, 0x54, 0x45, 0x53, 0x54,
		0x12, 0x08, 0x0a, 0x04, 0x7f, 0x00, 0x00, 0x00, 0x10, 0x08,
	}
	geositeFixture = []byte{
		0x0a, 0x1c, 0x0a, 0x04, 0x54, 0x45, 0x53, 0x54,
		0x12, 0x14, 0x08, 0x03, 0x12, 0x10, 0x74, 0x65, 0x73, 0x74,
		0x2e, 0x65, 0x78, 0x61, 0x6d, 0x70, 0x6c, 0x65, 0x2e, 0x63, 0x6f, 0x6d,
	}
)

// 保留 benchmark 原有的数据目录指向（本地若有完整 geoip.dat/geosite.dat，
// BenchmarkLoad* 仍可从 test/temp 读）；但绝不在这里下载——下载曾导致 CI
// 因网络 503 在 init 阶段 panic。Decode 的两个测试用内嵌夹具，与该目录无关。
func TestMain(m *testing.M) {
	wd, err := os.Getwd()
	if err == nil {
		_ = os.Setenv("TROJAN_GO_LOCATION_ASSET", filepath.Join(wd, "..", "..", "test", "temp"))
	}
	os.Exit(m.Run())
}

// writeFixture 把内嵌夹具写进测试自己的临时目录，并返回 .dat 文件路径。
// 用 t.TempDir 而非共享的 test/temp：每个测试独立、并行安全、无跨 run 缓存。
func writeFixture(t *testing.T, name string, data []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
	return path
}

func TestDecodeGeoIP(t *testing.T) {
	filename := writeFixture(t, "geoip.dat", geoipFixture)
	result, err := geodata.Decode(filename, "test")
	if err != nil {
		t.Fatal(err)
	}

	expected := []byte{10, 4, 84, 69, 83, 84, 18, 8, 10, 4, 127, 0, 0, 0, 16, 8}
	if !bytes.Equal(result, expected) {
		t.Errorf("failed to load geoip:test, expected: %v, got: %v", expected, result)
	}
}

func TestDecodeGeoSite(t *testing.T) {
	filename := writeFixture(t, "geosite.dat", geositeFixture)
	result, err := geodata.Decode(filename, "test")
	if err != nil {
		t.Fatal(err)
	}

	expected := []byte{10, 4, 84, 69, 83, 84, 18, 20, 8, 3, 18, 16, 116, 101, 115, 116, 46, 101, 120, 97, 109, 112, 108, 101, 46, 99, 111, 109}
	if !bytes.Equal(result, expected) {
		t.Errorf("failed to load geosite:test, expected: %v, got: %v", expected, result)
	}
}

func BenchmarkLoadGeoIP(b *testing.B) {
	m1 := runtime.MemStats{}
	m2 := runtime.MemStats{}

	loader := geodata.NewGeodataLoader()

	runtime.ReadMemStats(&m1)
	cn, _ := loader.LoadGeoIP("cn")
	private, _ := loader.LoadGeoIP("private")
	runtime.KeepAlive(cn)
	runtime.KeepAlive(private)
	runtime.ReadMemStats(&m2)

	b.ReportMetric(float64(m2.Alloc-m1.Alloc)/1024, "KiB(GeoIP-Alloc)")
	b.ReportMetric(float64(m2.TotalAlloc-m1.TotalAlloc)/1024/1024, "MiB(GeoIP-TotalAlloc)")
}

func BenchmarkLoadGeoSite(b *testing.B) {
	m3 := runtime.MemStats{}
	m4 := runtime.MemStats{}

	loader := geodata.NewGeodataLoader()

	runtime.ReadMemStats(&m3)
	cn, _ := loader.LoadGeoSite("cn")
	notcn, _ := loader.LoadGeoSite("geolocation-!cn")
	private, _ := loader.LoadGeoSite("private")
	runtime.KeepAlive(cn)
	runtime.KeepAlive(notcn)
	runtime.KeepAlive(private)
	runtime.ReadMemStats(&m4)

	b.ReportMetric(float64(m4.Alloc-m3.Alloc)/1024/1024, "MiB(GeoSite-Alloc)")
	b.ReportMetric(float64(m4.TotalAlloc-m3.TotalAlloc)/1024/1024, "MiB(GeoSite-TotalAlloc)")
}
