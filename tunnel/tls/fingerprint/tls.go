package fingerprint

import (
	"crypto/tls"
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
