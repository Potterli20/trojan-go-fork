package logger

import (
	"context"
	"sync"

	"go.uber.org/zap"
)

// OpenTelemetryProvider interface for clean abstraction
type OpenTelemetryProvider interface {
	CreateLogger(config Config, standardLogger *zap.Logger) (*zap.Logger, error)
	Shutdown(ctx context.Context) error
}

// OpenTelemetryConfig holds OpenTelemetry logging configuration
type OpenTelemetryConfig struct {
	Enabled        bool              `json:"enabled" yaml:"enabled" mapstructure:"enabled"`
	Endpoint       string            `json:"endpoint" yaml:"endpoint" mapstructure:"endpoint"`
	ServiceName    string            `json:"service_name" yaml:"service_name" mapstructure:"service_name"`
	ServiceVersion string            `json:"service_version,omitempty" yaml:"service_version,omitempty" mapstructure:"service_version,omitempty"`
	Environment    string            `json:"environment,omitempty" yaml:"environment,omitempty" mapstructure:"environment,omitempty"`
	Hostname       string            `json:"hostname,omitempty" yaml:"hostname,omitempty" mapstructure:"hostname,omitempty"`
	Headers        map[string]string `json:"headers,omitempty" yaml:"headers,omitempty" mapstructure:"headers,omitempty"`
	Insecure       bool              `json:"insecure,omitempty" yaml:"insecure,omitempty" mapstructure:"insecure,omitempty"`
	Protocol       string            `json:"protocol,omitempty" yaml:"protocol,omitempty" mapstructure:"protocol,omitempty"` // "grpc" or "http"
}

// OpenTelemetryManager manages OpenTelemetry providers in a thread-safe way
type OpenTelemetryManager struct {
	mu       sync.RWMutex
	provider OpenTelemetryProvider
}

var otelManager = &OpenTelemetryManager{}

// RegisterOpenTelemetryProvider allows external packages to register OpenTelemetry implementation
func RegisterOpenTelemetryProvider(provider OpenTelemetryProvider) {
	otelManager.mu.Lock()
	defer otelManager.mu.Unlock()
	otelManager.provider = provider
}

// getOpenTelemetryProvider returns the registered provider in a thread-safe way
func getOpenTelemetryProvider() OpenTelemetryProvider {
	otelManager.mu.RLock()
	defer otelManager.mu.RUnlock()
	return otelManager.provider
}

// ShutdownOpenTelemetryLogger gracefully shuts down the OpenTelemetry logger
func ShutdownOpenTelemetryLogger(ctx context.Context) error {
	provider := getOpenTelemetryProvider()
	if provider != nil {
		return provider.Shutdown(ctx)
	}
	return nil
}

// IsOpenTelemetryActive checks if OpenTelemetry is currently active and working
func IsOpenTelemetryActive() bool {
	otelManager.mu.RLock()
	defer otelManager.mu.RUnlock()
	return otelManager.provider != nil
}
