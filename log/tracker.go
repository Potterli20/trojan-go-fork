package log

import (
	"fmt"
	"sync/atomic"
	"time"
)

var connIDCounter uint64

func generateConnID() string {
	id := atomic.AddUint64(&connIDCounter, 1)
	return fmt.Sprintf("conn-%d-%d", time.Now().UnixNano()%1000000, id)
}

type ConnectionTracker struct {
	connID    string
	module    string
	action    string
	startTime time.Time
	endTime   time.Time
	fields    map[string]interface{}
}

func NewConnectionTracker(module, action string) *ConnectionTracker {
	return &ConnectionTracker{
		connID:    generateConnID(),
		module:    module,
		action:    action,
		startTime: time.Now(),
		fields:    make(map[string]interface{}),
	}
}

func NewConnectionTrackerWithID(connID, module, action string) *ConnectionTracker {
	return &ConnectionTracker{
		connID:    connID,
		module:    module,
		action:    action,
		startTime: time.Now(),
		fields:    make(map[string]interface{}),
	}
}

func (t *ConnectionTracker) ConnID() string {
	return t.connID
}

func (t *ConnectionTracker) StartTime() time.Time {
	return t.startTime
}

func (t *ConnectionTracker) WithField(key string, value interface{}) *ConnectionTracker {
	t.fields[key] = value
	return t
}

func (t *ConnectionTracker) WithFields(fields map[string]interface{}) *ConnectionTracker {
	for k, v := range fields {
		t.fields[k] = v
	}
	return t
}

func (t *ConnectionTracker) Success() error {
	t.endTime = time.Now()
	if !ShouldLog(InfoLevel) {
		return nil
	}
	duration := t.endTime.Sub(t.startTime)
	durationMs := float64(duration.Nanoseconds()) / 1e6

	Logf(InfoLevel, "[%s] [conn=%s] action=%s status=success start=%s end=%s duration=%.2fms %v",
		t.module, t.connID, t.action,
		t.startTime.Format("2006-01-02 15:04:05.000"),
		t.endTime.Format("2006-01-02 15:04:05.000"),
		durationMs, t.fields)
	return nil
}

func (t *ConnectionTracker) Error(err error) error {
	t.endTime = time.Now()
	if !ShouldLog(ErrorLevel) {
		return err
	}
	duration := t.endTime.Sub(t.startTime)
	durationMs := float64(duration.Nanoseconds()) / 1e6

	Logf(ErrorLevel, "[%s] [conn=%s] action=%s status=error start=%s end=%s duration=%.2fms error=%v %v",
		t.module, t.connID, t.action,
		t.startTime.Format("2006-01-02 15:04:05.000"),
		t.endTime.Format("2006-01-02 15:04:05.000"),
		durationMs, err, t.fields)
	return err
}

func (t *ConnectionTracker) Destroy(reason string, sentBytes, recvBytes uint64) {
	t.endTime = time.Now()
	if !ShouldLog(InfoLevel) {
		return
	}
	duration := t.endTime.Sub(t.startTime)
	durationMs := float64(duration.Nanoseconds()) / 1e6

	Logf(InfoLevel, "[%s] [conn=%s] action=destroy status=closed start=%s end=%s duration=%.2fms reason=%s sent=%d recv=%d",
		t.module, t.connID,
		t.startTime.Format("2006-01-02 15:04:05.000"),
		t.endTime.Format("2006-01-02 15:04:05.000"),
		durationMs, reason, sentBytes, recvBytes)
}
