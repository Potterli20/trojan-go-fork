package log

import (
	"errors"
	"testing"
	"time"
)

func TestLogConnectionEvent(t *testing.T) {
	SetLogLevelInternal(OffLevel)

	fields := ConnectionLogFields{
		Module:     "Test",
		Action:     "Connect",
		RemoteAddr: "192.168.1.1",
		TargetAddr: "10.0.0.1",
		Duration:   time.Millisecond * 100,
		Success:    true,
	}

	LogConnectionEvent(InfoLevel, fields)

	SetLogLevelInternal(AllLevel)

	LogConnectionEvent(ErrorLevel, ConnectionLogFields{
		Module:     "Test",
		Action:     "Connect",
		RemoteAddr: "192.168.1.1",
		TargetAddr: "10.0.0.1",
		Duration:   time.Millisecond * 50,
		Error:      errors.New("connection refused"),
		Success:    false,
	})
}

func TestLogConnectionEventWithLogLevelFilter(t *testing.T) {
	SetLogLevelInternal(ErrorLevel)

	LogConnectionEvent(DebugLevel, ConnectionLogFields{
		Module:  "Test",
		Action:  "Connect",
		Success: true,
	})
}
