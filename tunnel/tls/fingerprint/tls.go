package fingerprint

import (
	"crypto/tls"
	
	"github.com/Potterli20/trojan-go-fork/log"
)


func ParseCipher(s []string) []uint32 {
	all := tls.CipherSuites()
	var result []uint32
	for _, p := range s {
		found := true
		for _, q := range all {
			if q.Name == p {
				result = append(result, q.ID)
				break
			}
			if !found {
				log.Warn("invalid cipher suite", p, "skipped")
			}
		}
	}
	return result
}
