package simplelog

import (
	"bytes"
	"strings"
	"testing"

	golog "log"
)

func TestSanitizeLogInput(t *testing.T) {
	tests := []struct {
		name         string
		input        []any
		want         []any
		contains     []string
		notContains  []string
	}{
		{
			name:        "removes newlines from user input",
			input:       []any{"user input with\nnewline"},
			notContains: []string{"\n"},
		},
		{
			name:        "removes carriage returns from user input",
			input:       []any{"user input with\r carriage return"},
			notContains: []string{"\r"},
		},
		{
			name:        "prevents log forging attack",
			input:       []any{"[ERROR] fake error\n[INFO] fake info"},
			notContains: []string{"\n[INFO]", "\n[ERROR]"},
		},
		{
			name:     "escapes HTML special characters",
			input:    []any{"<script>alert('xss')</script>"},
			contains: []string{"&lt;"},
		},
		{
			name:        "handles mixed data types",
			input:       []any{"test", 123, "line1\nline2"},
			notContains: []string{"\n"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeLogInput(tt.input)
			for _, item := range result {
				if str, ok := item.(string); ok {
					for _, s := range tt.notContains {
						if strings.Contains(str, s) {
							t.Errorf("sanitizeLogInput() = %v, should not contain %q", str, s)
						}
					}
					for _, s := range tt.contains {
						if !strings.Contains(str, s) {
							t.Errorf("sanitizeLogInput() = %v, should contain %q", str, s)
						}
					}
				}
			}
		})
	}
}

func TestDebugLogSanitization(t *testing.T) {
	var buf bytes.Buffer
	golog.SetOutput(&buf)
	defer golog.SetOutput(nil)

	logger := &SimpleLogger{}
	logger.SetLogLevel(0)

	logger.Debug("test\n[INFO] fake log entry")

	logOutput := buf.String()
	if strings.Contains(logOutput, "\n[INFO]") {
		t.Errorf("Debug() log output = %q, should not contain forged log entry", logOutput)
	}
}
