package logger

import (
	"go.uber.org/zap"
)

func ReplaceGlobals(logger *zap.Logger) func() {
	return zap.ReplaceGlobals(logger)
}

func SetGlobalConfig(config Config) func() {
	logger := configureAndBuildLogger(config)
	return ReplaceGlobals(logger)
}

func L() *zap.Logger {
	return zap.L()
}

func S() *zap.SugaredLogger {
	return zap.S()
}

func Info(msg string) {
	L().Info(msg)
}

func Debug(msg string) {
	L().Debug(msg)
}

func Warn(msg string) {
	L().Warn(msg)
}

func Error(msg string) {
	L().Error(msg)
}

func DPanic(msg string) {
	L().DPanic(msg)
}

func Panic(msg string) {
	L().Panic(msg)
}

func Fatal(msg string) {
	L().Fatal(msg)
}

func Infof(template string, args ...any) {
	S().Infof(template, args...)
}

func Debugf(template string, args ...any) {
	S().Debugf(template, args...)
}

func Warnf(template string, args ...any) {
	S().Warnf(template, args...)
}

func Errorf(template string, args ...any) {
	S().Errorf(template, args...)
}

func DPanicf(template string, args ...any) {
	S().DPanicf(template, args...)
}

func Panicf(template string, args ...any) {
	S().Panicf(template, args...)
}

func Fatalf(template string, args ...any) {
	S().Fatalf(template, args...)
}

func Sync() error {
	// Sync global logger
	err := L().Sync()
	if err != nil {
		return err
	}
	// Sync global sugar logger
	return S().Sync()
}
