package golog

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Potterli20/trojan-go-fork/log"
)

func TestSanitizeLogInput(t *testing.T) {
	tests := []struct {
		name        string
		input       []any
		contains    []string
		notContains []string
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
			result := log.SanitizeLogInput(tt.input)
			for _, item := range result {
				if str, ok := item.(string); ok {
					for _, s := range tt.notContains {
						if strings.Contains(str, s) {
							t.Errorf("SanitizeLogInput() = %v, should not contain %q", str, s)
						}
					}
					for _, s := range tt.contains {
						if !strings.Contains(str, s) {
							t.Errorf("SanitizeLogInput() = %v, should contain %q", str, s)
						}
					}
				}
			}
		})
	}
}

// TestDebugLogSanitization verifies that calling golog.Logger methods directly
// (bypassing the global log package) still performs input sanitization.
func TestDebugLogSanitization(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&bufferWriter{&buf})
	logger.SetLogLevel(0)

	// Call logger.Debug directly (NOT log.Debug) to verify golog layer sanitizes
	logger.Debug("test\n[INFO] fake log entry")

	logOutput := buf.String()
	if strings.Contains(logOutput, "\n[INFO]") {
		t.Errorf("Debug() direct call log output = %q, should not contain forged log entry", logOutput)
	}
}

// TestDirectLoggerSanitization verifies all log levels sanitize input when the
// logger is used directly (not via the global log.* package).
func TestDirectLoggerSanitization(t *testing.T) {
	tests := []struct {
		name   string
		logFn  func(*Logger, string)
	}{
		{
			name: "Error direct sanitization",
			logFn: func(l *Logger, msg string) {
				l.Error(msg)
			},
		},
		{
			name: "Errorf direct sanitization",
			logFn: func(l *Logger, msg string) {
				l.Errorf("[module] %s", msg)
			},
		},
		{
			name: "Warn direct sanitization",
			logFn: func(l *Logger, msg string) {
				l.Warn(msg)
			},
		},
		{
			name: "Warnf direct sanitization",
			logFn: func(l *Logger, msg string) {
				l.Warnf("[module] %s", msg)
			},
		},
		{
			name: "Info direct sanitization",
			logFn: func(l *Logger, msg string) {
				l.Info(msg)
			},
		},
		{
			name: "Infof direct sanitization",
			logFn: func(l *Logger, msg string) {
				l.Infof("[module] %s", msg)
			},
		},
		{
			name: "Debug direct sanitization",
			logFn: func(l *Logger, msg string) {
				l.Debug(msg)
			},
		},
		{
			name: "Debugf direct sanitization",
			logFn: func(l *Logger, msg string) {
				l.Debugf("[module] %s", msg)
			},
		},
		{
			name: "Trace direct sanitization",
			logFn: func(l *Logger, msg string) {
				l.Trace(msg)
			},
		},
		{
			name: "Tracef direct sanitization",
			logFn: func(l *Logger, msg string) {
				l.Tracef("[module] %s", msg)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := New(&bufferWriter{&buf})
			logger.SetLogLevel(0) // AllLevel = 0
			logger.WithoutColor()
			logger.WithoutTimestamp()

			// Inject forged log entry via user input
			tt.logFn(logger, "userdata\n[INFO] FORGED LOG ENTRY")

			logOutput := buf.String()
			if strings.Contains(logOutput, "\n[INFO] FORGED") {
				t.Errorf("%s failed to sanitize: output = %q", tt.name, logOutput)
			}
		})
	}
}

type bufferWriter struct {
	*bytes.Buffer
}

func (w *bufferWriter) Fd() uintptr {
	return 1
}