package fingerprint

import (
	"slices"

	"crypto/tls"
	"github.com/Potterli20/trojan-go-fork/common"
	"strings"

	utls "github.com/refraction-networking/utls"
)

func ParseCipher(s []string) []uint16 {
	all := tls.CipherSuites()
	var result []uint16
	for _, p := range s {
		// 与 ValidateCipher 同口径 TrimSpace：配置里 "cipher1: cipher2" 的空格很常见，
		// 校验放行却在这里被丢弃的话，用户配的套件就没生效。curve 的两侧也都 trim。
		p = strings.TrimSpace(p)
		for _, q := range all {
			if q.Name == p {
				result = append(result, q.ID)
				break
			}
		}
	}
	return result
}

// ValidateCipher 在启动期拒绝未知 cipher 名字。ParseCipher 对认不出的名字静默跳过：
// 配了非空 cipher 却解析出 0 个套件时，tls.Config.CipherSuites 拿到 nil 会走 Go 默认
// 全套，用户以为收紧了加密套件、实际没有任何限制，握手照常成功也不报错——正是
// curve_preferences 已修过的同型"接受但不生效"。空项按既有口径跳过。
func ValidateCipher(names []string) error {
	all := tls.CipherSuites()
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		found := false
		for _, q := range all {
			if q.Name == name {
				found = true
				break
			}
		}
		if !found {
			return common.NewErrorf("unknown cipher suite %q (supported: %s)", name, knownCipherNames())
		}
	}
	return nil
}

func knownCipherNames() string {
	all := tls.CipherSuites()
	names := make([]string, 0, len(all))
	for _, q := range all {
		names = append(names, q.Name)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
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
	// 与 curveNameMap 同集合：四个后量子组都在（MLKEM1024 是 Go 1.27 新加的纯
	// ML-KEM 密钥交换，码点 514，与 stdlib 一致）。缺一项的后果不是报错而是
	// **静默丢掉**——带 fingerprint 的客户端以为开了后量子，ClientHello 里却没有。
	"X25519MLKEM768":     utls.X25519MLKEM768,
	"SecP256r1MLKEM768":  utls.SecP256r1MLKEM768,
	"SecP384r1MLKEM1024": utls.SecP384r1MLKEM1024,
	"MLKEM1024":          utls.MLKEM1024,
	"X25519":             utls.X25519,
	"CurveP256":          utls.CurveP256,
	"CurveP384":          utls.CurveP384,
	"CurveP521":          utls.CurveP521,
}

// ValidateCurvePreferences 在启动期拒绝未知名称。
// 两个 Parse* 过去对认不出的名字直接 continue：拼错的 "X25519MLKEM768"（少个 8）
// 或只有 stdlib 认、utls 不认的名字，都会被无声丢掉——用户以为密钥交换被改成
// 后量子，实际用的是 Go 默认集合，而握手照样成功，没有任何症状。
func ValidateCurvePreferences(names []string) error {
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		std, okStd := curveNameMap[name]
		ut, okUtls := utlsCurveNameMap[name]
		if !okStd || !okUtls {
			return common.NewErrorf("unknown curve preference %q (supported: %s)", name, strings.Join(knownCurveNames(), ", "))
		}
		if uint16(std) != uint16(ut) {
			// 两侧码点必须一致，否则带 fingerprint 与不带的两条路会发出不同的密钥交换集合。
			return common.NewErrorf("curve preference %q maps to different code points in crypto/tls and utls: %d vs %d",
				name, uint16(std), uint16(ut))
		}
	}
	return nil
}

func knownCurveNames() []string {
	names := make([]string, 0, len(curveNameMap))
	for name := range curveNameMap {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
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
