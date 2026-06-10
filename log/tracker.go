package log

import "time"

type ConnectionTracker struct {
	module    string
	action    string
	startTime time.Time
	fields    map[string]interface{}
}

func NewConnectionTracker(module, action string) *ConnectionTracker {
	return &ConnectionTracker{
		module:    module,
		action:    action,
		startTime: time.Now(),
		fields:    make(map[string]interface{}),
	}
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

func (t *ConnectionTracker) Success() {
	if !ShouldLog(InfoLevel) {
		return
	}
	duration := time.Since(t.startTime)
	if len(t.fields) > 0 {
		Logf(InfoLevel, "[%s] %s succeeded - duration:%v %v",
			t.module, t.action, duration, t.fields)
	} else {
		Logf(InfoLevel, "[%s] %s succeeded - duration:%v",
			t.module, t.action, duration)
	}
}

func (t *ConnectionTracker) Error(err error) {
	if !ShouldLog(ErrorLevel) {
		return
	}
	duration := time.Since(t.startTime)
	if len(t.fields) > 0 {
		Logf(ErrorLevel, "[%s] %s failed - duration:%v error:%v %v",
			t.module, t.action, duration, err, t.fields)
	} else {
		Logf(ErrorLevel, "[%s] %s failed - duration:%v error:%v",
			t.module, t.action, duration, err)
	}
}
