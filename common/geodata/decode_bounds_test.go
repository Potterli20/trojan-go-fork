package geodata_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Potterli20/trojan-go-fork/common/geodata"
)

// geoIPEntry 造一条最小可用的 GeoIP 条目：
//
//	0A <L>            外层 GeoIPList.field 与其 varint 长度
//	  0A <C> <code>   GeoIP.country_code（字段 1，长度定界字符串）
//	  12 <n> <cidr>   GeoIP.cidr（字段 2）
//
// 长度都控制在 128 以内，保证 varint 只占一字节，和解析器的逐步读法对齐。
func geoIPEntry(code string, cidr []byte) []byte {
	inner := []byte{0x0A, byte(len(code))}
	inner = append(inner, code...)
	inner = append(inner, 0x12, byte(len(cidr)))
	inner = append(inner, cidr...)
	outer := []byte{0x0A, byte(len(inner))}
	return append(outer, inner...)
}

var testCIDR = []byte{0x0A, 0x04, 127, 0, 0, 0, 0x10, 0x08} // CIDR{127.0.0.0/8}

func TestEmitBytesLoadsValidEntry(t *testing.T) {
	data := append(geoIPEntry("cc", testCIDR), geoIPEntry("dd", testCIDR)...)
	got, err := geodata.EmitBytes(bytes.NewReader(data), "cc")
	if err != nil {
		t.Fatalf("合法条目应当能取出来，实际报错：%v", err)
	}
	want := geoIPEntry("cc", testCIDR)[2:] // 去掉外层 0A <L>，返回值是条目本体
	if !bytes.Equal(got, want) {
		t.Fatalf("取出的字节不对，期望 %v，实际 %v", want, got)
	}
}

func TestEmitBytesMissingCode(t *testing.T) {
	data := append(geoIPEntry("cc", testCIDR), geoIPEntry("dd", testCIDR)...)
	_, err := geodata.EmitBytes(bytes.NewReader(data), "zz")
	if !errors.Is(err, geodata.ErrCodeNotFound) {
		t.Fatalf("不存在的 code 应当返回 ErrCodeNotFound，实际：%v", err)
	}
}

// TestEmitBytesRejectsLengthBeyondFileEnd 盯的是一次"按文件里的长度字段直接分配内存"：
// 解析器把 varint 长度原样拿来 make([]byte, n)，既不对照剩余文件长度也不做上限检查。
// 于是一个损坏（或恶意）的 geoip.dat/geosite.dat 只要声称某条目有 2^60 字节，
// 就得到 make([]byte, 1<<60) —— runtime 直接 panic("makeslice: len out of range")，
// 或者在能分配下来的机器上把内存吃光。router 在启动时按配置加载这两个文件，
// 等于"一次坏下载打崩整个进程"。
func TestEmitBytesRejectsLengthBeyondFileEnd(t *testing.T) {
	huge := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x10} // varint(2^60)
	inner := []byte{0x0A, 0x02, 'c', 'c', 0x12, byte(len(testCIDR))}
	inner = append(inner, testCIDR...)
	data := []byte{0x0A}
	data = append(data, huge...) // 外层声称长度 = 2^60
	data = append(data, inner...)

	var err error
	panicked := func() (recovered any) {
		defer func() { recovered = recover() }()
		_, err = geodata.EmitBytes(bytes.NewReader(data), "cc")
		return nil
	}()
	if panicked != nil {
		t.Fatalf("声称 2^60 字节的条目直接把进程炸了（make([]byte, 1<<60)）：%v", panicked)
	}
	if err == nil {
		t.Fatal("声称 2^60 字节的条目居然被\"成功\"读出来了——长度上界检查没有生效")
	}
	if !errors.Is(err, geodata.ErrInvalidGeodataLength) {
		t.Fatalf("期望 ErrInvalidGeodataLength，实际：%v", err)
	}
}

// TestEmitBytesSurvivesShortReads 修的是同一族的另一半：原来用 f.Read() 一次读
// advancedN 字节，然后拿 bytesRead != len 当硬错误。io.Reader 的契约明确允许
// "短读不算错误"，所以任何一次短读（大条目、或下面这种一次只给一字的读取器）
// 都会让合法的 .dat 报 ErrFailedToReadExpectedLenBytes。
func TestEmitBytesSurvivesShortReads(t *testing.T) {
	data := append(geoIPEntry("cc", testCIDR), geoIPEntry("dd", testCIDR)...)
	got, err := geodata.EmitBytes(&oneByteReader{r: bytes.NewReader(data)}, "cc")
	if err != nil {
		t.Fatalf("一次只读一字节的合法文件应当能解析，实际报错：%v", err)
	}
	want := geoIPEntry("cc", testCIDR)[2:]
	if !bytes.Equal(got, want) {
		t.Fatalf("短读场景下取出的字节不对，期望 %v，实际 %v", want, got)
	}
}

// oneByteReader 每次最多只交出 1 字节——真实 io.Reader 允许的行为。
type oneByteReader struct {
	r io.ReadSeeker
}

func (o *oneByteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return o.r.Read(p[:1])
}

func (o *oneByteReader) Seek(offset int64, whence int) (int64, error) {
	return o.r.Seek(offset, whence)
}

// TestDecodeRealAssetBigEntry 是上界修复的阳性对照：真实 geosite.dat 里
// geolocation-!cn 是最大的一批条目之一，必须照样能取出来。
// 文件不在（离线 CI 没下载下来）就跳过，不算失败。
func TestDecodeRealAssetBigEntry(t *testing.T) {
	path := filepath.Join("..", "..", "test", "temp", "geosite.dat")
	if _, err := os.Stat(path); err != nil {
		t.Skip("没有本地 geosite.dat，跳过真实文件对照")
	}
	got, err := geodata.Decode(path, "geolocation-!cn")
	if err != nil {
		t.Fatalf("合法的大条目解析失败：%v", err)
	}
	if len(got) < 1000 {
		t.Fatalf("geolocation-!cn 只取回 %d 字节，明显不对", len(got))
	}
}
