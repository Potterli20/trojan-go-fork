package fingerprint

import (
	"crypto/tls"
	"strings"

	utls "github.com/refraction-networking/utls"
)

func ParseCipher(s []string) []uint16 {
	all := tls.CipherSuites()
	var result []uint16
	for _, p := range s {
		for _, q := range all {
			if q.Name == p {
				result = append(result, q.ID)
				break
			}
		}
	}
	return result
}

// curveNameMap 将配置中的字符串名称映射到标准库 tls.CurveID
var curveNameMap = map[string]tls.CurveID{
	"X25519MLKEM768":     tls.X25519MLKEM768,
	"SecP256r1MLKEM768":  tls.SecP256r1MLKEM768,
	"SecP384r1MLKEM1024": tls.SecP384r1MLKEM1024,
	"MLKEM1024":          tls.MLKEM1024,
	"X25519":             tls.X25519,
	"CurveP256":          tls.CurveP256,
	"CurveP384":          tls.CurveP384,
	"CurveP521":          tls.CurveP521,
}

// utlsCurveNameMap 将配置中的字符串名称映射到 utls.CurveID
var utlsCurveNameMap = map[string]utls.CurveID{
	"X25519MLKEM768": utls.X25519MLKEM768,
	"X25519":         utls.X25519,
	"CurveP256":      utls.CurveP256,
	"CurveP384":      utls.CurveP384,
	"CurveP521":      utls.CurveP521,
}

// ParseCurvePreferences 将配置中的曲线名称列表解析为标准库 tls.CurveID 列表
func ParseCurvePreferences(names []string) []tls.CurveID {
	var result []tls.CurveID
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if id, ok := curveNameMap[name]; ok {
			result = append(result, id)
		}
	}
	return result
}

// ParseUTLSCurvePreferences 将配置中的曲线名称列表解析为 utls.CurveID 列表
func ParseUTLSCurvePreferences(names []string) []utls.CurveID {
	var result []utls.CurveID
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if id, ok := utlsCurveNameMap[name]; ok {
			result = append(result, id)
		}
	}
	return result
}
