package fingerprint

import (
	"os"
	"strings"
	"testing"
)

// 两张名字表必须覆盖同一组名称、且映射到同一个码点。
// 之前 utls 侧缺了 SecP256r1MLKEM768 / SecP384r1MLKEM1024 / MLKEM1024：带 fingerprint 的
// 客户端把 curve_preferences 里的后量子组**静默丢掉**（Parse 对认不出的名字 continue），
// 于是同一个配置在"带指纹"和"不带指纹"两条路上发出完全不同的密钥交换集合，而握手都成功。
func TestCurveNameMapsAgreeOnNamesAndCodePoints(t *testing.T) {
	for name, std := range curveNameMap {
		ut, ok := utlsCurveNameMap[name]
		if !ok {
			t.Errorf("%q 只在 crypto/tls 侧有：带 fingerprint 的客户端会把它无声丢掉", name)
			continue
		}
		if uint16(std) != uint16(ut) {
			t.Errorf("%q 两侧码点不一致：%d vs %d", name, uint16(std), uint16(ut))
		}
	}
	for name := range utlsCurveNameMap {
		if _, ok := curveNameMap[name]; !ok {
			t.Errorf("%q 只在 utls 侧有：不带走标准库 TLS 的路径解析不出来", name)
		}
	}
}

// Go 1.27 在 crypto/tls 里加了 MLKEM1024（纯 ML-KEM 密钥交换，码点 514）。
// 这里钉住它确实可配、且两侧一致——它是本轮唯一新增的后量子能力。
func TestPostQuantumCurvesAreConfigurable(t *testing.T) {
	names := []string{"X25519MLKEM768", "SecP256r1MLKEM768", "SecP384r1MLKEM1024", "MLKEM1024"}
	if err := ValidateCurvePreferences(names); err != nil {
		t.Fatalf("四个后量子组应当全部可用：%v", err)
	}
	std := ParseCurvePreferences(names)
	ut := ParseUTLSCurvePreferences(names)
	if len(std) != len(names) || len(ut) != len(names) {
		t.Fatalf("解析结果有丢失：crypto/tls %d 个、utls %d 个，期望各 %d 个",
			len(std), len(ut), len(names))
	}
	if curveNameMap["MLKEM1024"] != 514 {
		t.Errorf("MLKEM1024 的码点是 %d，期望 514（IANA TLS_SUPPORTED_GROUPS）", uint16(curveNameMap["MLKEM1024"]))
	}
}

// 未知名字必须报错。静默跳过就是"接受但不生效"。
func TestValidateCurvePreferencesRejectsUnknownNames(t *testing.T) {
	for _, name := range []string{"X25519MLKEM7688", "x25519", "secp256k1", "ML-KEM-1024"} {
		err := ValidateCurvePreferences([]string{name})
		if err == nil {
			t.Errorf("%q 被静默接受：过去 Parse* 直接 continue，配置拼错没有任何症状", name)
			continue
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("%q 的报错没有指名道姓：%v", name, err)
		}
	}
	// 正向对照：空列表（用 Go 默认）与合法列表都不该报错
	if err := ValidateCurvePreferences(nil); err != nil {
		t.Errorf("留空应当放行：%v", err)
	}
	if err := ValidateCurvePreferences([]string{"X25519", " CurveP384 ", ""}); err != nil {
		t.Errorf("合法列表报错：%v", err)
	}
}

// 接线：四个建栈点都必须调用校验，否则谓词只是摆在那里。
func TestCurveValidationIsWiredIntoEveryBuilder(t *testing.T) {
	sites := map[string]string{
		"../client.go":         "ValidateCurvePreferences(cfg.TLS.CurvePreferences)",
		"../server.go":         "ValidateCurvePreferences(cfg.TLS.CurvePreferences)",
		"../../quic/client.go": "ValidateCurvePreferences(tlsCfg.TLS.CurvePreferences)",
		"../../quic/server.go": "ValidateCurvePreferences(tlsCfg.TLS.CurvePreferences)",
	}
	for file, want := range sites {
		src := readFile(t, file)
		if !strings.Contains(src, want) {
			t.Errorf("%s 没有调用 ValidateCurvePreferences：曲线偏好写错仍然会被静默丢掉", file)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s: %v（测试的工作目录应当是包目录，若改动过请同步这里）", path, err)
	}
	return string(b)
}
