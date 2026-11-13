package log

import (
	"strings"
	"testing"
)

var sanitizeSamples = map[string]string{
	"connection": "socks connect request from 10.0.0.2:54321 metadata &{CONNECT 1 tcp example.org 443}",
	"long-path":  "tls failed to handshake with https://cdn.example.org/api/v2/assets/0123456789abcdef0123456789abcdef/thumb",
	"commit":     "Commit: 6a0bc3511234567890abcdef1234567890abcdef",
	"secret":     "dial failed: password=hunter2 for user admin",
	"jwt":        "authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload.sig",
}

func BenchmarkSanitizeString(b *testing.B) {
	for name, line := range sanitizeSamples {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = SanitizeString(line)
			}
		})
	}
}

func BenchmarkObfuscateSensitiveData(b *testing.B) {
	for name, line := range sanitizeSamples {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = ObfuscateSensitiveData(line)
			}
		})
	}
}

func TestSanitizeSamplesAreStable(t *testing.T) {
	for name, line := range sanitizeSamples {
		got := SanitizeString(line)
		if strings.Contains(got, "hunter2") || strings.Contains(got, "eyJhbGciOiJ") {
			t.Fatalf("%s: 凭据没有被遮掉: %q", name, got)
		}
	}
}