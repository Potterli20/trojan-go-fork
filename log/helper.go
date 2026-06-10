package log

import "time"

type ConnectionLogFields struct {
	Module     string
	Action     string
	RemoteAddr string
	TargetAddr string
	Duration   time.Duration
	Error      error
	Success    bool
}

func LogConnectionEvent(level LogLevel, fields ConnectionLogFields) {
	if !ShouldLog(level) {
		return
	}

	if fields.Error != nil {
		Logf(level, "[%s] %s failed - remote:%s target:%s duration:%v error:%v",
			fields.Module, fields.Action, fields.RemoteAddr,
			fields.TargetAddr, fields.Duration, fields.Error)
	} else {
		Logf(level, "[%s] %s succeeded - remote:%s target:%s duration:%v",
			fields.Module, fields.Action, fields.RemoteAddr,
			fields.TargetAddr, fields.Duration)
	}
}
