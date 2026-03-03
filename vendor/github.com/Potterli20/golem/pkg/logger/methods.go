package logger

import (
	"context"

	"go.uber.org/zap"
)

const (
	loggerKey    = "golem.logger"
	RequestIDKey = "request_id"
	// contextFieldKey is the field name used by otelzap bridge to detect context.Context
	// and automatically extract trace information for log-trace correlation
	contextFieldKey = "context"
)

func (l *Logger) Info(msg string) {
	l.logger.Info(msg)
}

func (l *Logger) Debug(msg string) {
	l.logger.Debug(msg)
}

func (l *Logger) Warn(msg string) {
	l.logger.Warn(msg)
}

func (l *Logger) Error(msg string) {
	l.logger.Error(msg)
}

func (l *Logger) DPanic(msg string) {
	l.logger.DPanic(msg)
}

func (l *Logger) Panic(msg string) {
	l.logger.Panic(msg)
}

func (l *Logger) Fatal(msg string) {
	l.logger.Fatal(msg)
}

func (l *Logger) Infof(template string, args ...any) {
	l.logger.Sugar().Infof(template, args...)
}

func (l *Logger) Debugf(template string, args ...any) {
	l.logger.Sugar().Debugf(template, args...)
}

func (l *Logger) Warnf(template string, args ...any) {
	l.logger.Sugar().Warnf(template, args...)
}

func (l *Logger) Errorf(template string, args ...any) {
	l.logger.Sugar().Errorf(template, args...)
}

func (l *Logger) DPanicf(template string, args ...any) {
	l.logger.Sugar().DPanicf(template, args...)
}

func (l *Logger) Panicf(template string, args ...any) {
	l.logger.Sugar().Panicf(template, args...)
}

func (l *Logger) Fatalf(template string, args ...any) {
	l.logger.Sugar().Fatalf(template, args...)
}

func (l *Logger) WithFields(fields ...zap.Field) *Logger {
	return &Logger{logger: l.logger.With(fields...)}
}

func (l *Logger) IsDebugEnabled() bool {
	return l.logger.Core().Enabled(zap.DebugLevel)
}

func GetLoggerFromContext(ctx context.Context) *Logger {
	logger, ok := ctx.Value(loggerKey).(*Logger)
	if !ok {
		newLogger := NewLogger()
		newLogger.Warnf("Logger had to be created without configuration because the context was missing")
		return newLogger
	}

	// Automatically add trace context if available
	return logger.withTraceContext(ctx)
}

// withTraceContext enhances the logger with trace context information from the provided context.
// This method uses the otelzap bridge's automatic context detection by adding the context as a field.
// The bridge will automatically extract trace_id and span_id when it detects a context.Context field.
func (l *Logger) withTraceContext(ctx context.Context) *Logger {
	if ctx == nil {
		return l
	}

	// The otelzap bridge automatically detects context.Context fields and extracts trace information
	// This is the recommended approach according to the otelzap documentation
	enhancedLogger := l.logger.With(zap.Any(contextFieldKey, ctx))
	return &Logger{logger: enhancedLogger}
}

func ContextWithLogger(ctx context.Context, logger *Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger) //nolint
}

// GetZapLogger returns the underlying zap.Logger instance
// This method is primarily intended for testing and advanced use cases
func (l *Logger) GetZapLogger() *zap.Logger {
	return l.logger
}
