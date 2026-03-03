# Logger Package

A structured logging package built on top of [uber-go/zap](https://github.com/uber-go/zap), providing both global and context-aware logging capabilities with sensible defaults and optional OpenTelemetry integration.

## Features

- Built on top of the high-performance zap logger
- Supports both structured and printf-style logging
- Context-aware logging
- Global and instance-based logging
- Configurable log levels and encoding formats
- Request ID tracking support
- **OpenTelemetry integration** for distributed tracing and observability
- Easy integration with existing applications

## Installation

```go
go get -u github.com/zondax/golem/pkg/logger
```

## Quick Start

```go
// Initialize with default configuration
logger.InitLogger(logger.Config{
    Level:    "info",
    Encoding: "json",
})

// Basic logging
logger.Info("Server started")
logger.Error("Connection failed")

// Structured logging with fields
log := logger.NewLogger(
    logger.Field{Key: "service", Value: "api"},
    logger.Field{Key: "version", Value: "1.0.0"},
)
log.Info("Service initialized")
```

## Configuration

### Logger Config

```go
type Config struct {
    Level         string                 `json:"level"`         // Logging level
    Encoding      string                 `json:"encoding"`      // Output format
    OpenTelemetry *OpenTelemetryConfig   `json:"opentelemetry"` // Optional OpenTelemetry config
}

type OpenTelemetryConfig struct {
    Enabled     bool              `json:"enabled"`      // Enable OpenTelemetry integration
    ServiceName string            `json:"service_name"` // Service name for telemetry
    Endpoint    string            `json:"endpoint"`     // OTLP endpoint URL
    Protocol    string            `json:"protocol"`     // Protocol: "http" or "grpc"
    Insecure    bool              `json:"insecure"`     // Use insecure connection
    Headers     map[string]string `json:"headers"`      // Additional headers
}
```

### Log Levels

Available log levels (in order of increasing severity):
- `debug`: Detailed information for debugging
- `info`: General operational information
- `warn`: Warning messages for potentially harmful situations
- `error`: Error conditions that should be addressed
- `dpanic`: Critical errors in development that cause panic
- `panic`: Critical errors that cause panic in production
- `fatal`: Fatal errors that terminate the program

### Encoding Formats

1. **JSON Format** (Default)
   - Recommended for production
   - Machine-readable structured output
   ```json
   {"level":"INFO","ts":"2024-03-20T10:00:00.000Z","msg":"Server started","service":"api"}
   ```

2. **Console Format**
   - Recommended for development
   - Human-readable output
   ```
   2024-03-20T10:00:00.000Z INFO Server started service=api
   ```

## OpenTelemetry Integration

The logger package provides seamless integration with OpenTelemetry for distributed tracing and observability platforms like SigNoz, Jaeger, and others.

### Basic OpenTelemetry Setup

```go
import (
    "github.com/zondax/golem/pkg/logger"
    "github.com/zondax/golem/pkg/logger/otel"
)

// Register the OpenTelemetry provider
provider := otel.NewProvider()
logger.RegisterOpenTelemetryProvider(provider)

// Configure logger with OpenTelemetry
config := logger.Config{
    Level:    "info",
    Encoding: "json",
    OpenTelemetry: &logger.OpenTelemetryConfig{
        Enabled:     true,
        ServiceName: "my-service",
        Endpoint:    "http://localhost:4318", // OTLP HTTP endpoint
        Protocol:    "http",                  // or "grpc"
        Insecure:    true,                    // for development
        Headers: map[string]string{
            "Authorization": "Bearer your-token",
        },
    },
}

// Initialize logger - logs will be sent to both console and OpenTelemetry
logger.InitLogger(config)

// Use logger normally - logs automatically go to OpenTelemetry
logger.Info("Service started")
```

### SigNoz Integration Example

```go
config := logger.Config{
    Level:    "info",
    Encoding: "json",
    OpenTelemetry: &logger.OpenTelemetryConfig{
        Enabled:     true,
        ServiceName: "my-api-service",
        Endpoint:    "http://signoz-otel-collector:4318", // SigNoz OTLP endpoint
        Protocol:    "http",
        Insecure:    false,
        Headers: map[string]string{
            "signoz-access-token": "your-signoz-token",
        },
    },
}
```

### Jaeger Integration Example

```go
config := logger.Config{
    Level:    "info",
    Encoding: "json",
    OpenTelemetry: &logger.OpenTelemetryConfig{
        Enabled:     true,
        ServiceName: "my-service",
        Endpoint:    "http://jaeger-collector:14268", // Jaeger OTLP endpoint
        Protocol:    "http",
        Insecure:    true,
    },
}
```

### gRPC Protocol Example

```go
config := logger.Config{
    Level:    "info",
    Encoding: "json",
    OpenTelemetry: &logger.OpenTelemetryConfig{
        Enabled:     true,
        ServiceName: "grpc-service",
        Endpoint:    "localhost:4317", // OTLP gRPC endpoint
        Protocol:    "grpc",
        Insecure:    true,
    },
}
```

### Graceful Shutdown

```go
import "context"

// Ensure proper cleanup of OpenTelemetry resources
defer func() {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    if err := logger.ShutdownOpenTelemetryLogger(ctx); err != nil {
        log.Printf("Error shutting down OpenTelemetry logger: %v", err)
    }
}()
```

## Advanced Usage

### Context-Aware Logging

```go
// Create a context with logger
ctx := context.Background()
log := logger.NewLogger(logger.Field{
    Key: logger.RequestIDKey,
    Value: "req-123",
})
ctx = logger.ContextWithLogger(ctx, log)

// Get logger from context
contextLogger := logger.GetLoggerFromContext(ctx)
contextLogger.Info("Processing request")
```

### Structured Logging with Fields

```go
log := logger.NewLogger()
log.WithFields(
    zap.String("user_id", "12345"),
    zap.String("action", "login"),
    zap.Int("attempt", 1),
).Info("User login attempt")
```

### Printf-Style Logging

```go
logger.Infof("Processing item %d of %d", current, total)
logger.Errorf("Failed to connect to %s: %v", host, err)
```

## Best Practices

1. **Use Structured Logging**
   ```go
   // Good
   log.WithFields(
       zap.String("user_id", "12345"),
       zap.String("action", "purchase"),
       zap.Float64("amount", 99.99),
   ).Info("Purchase completed")

   // Avoid
   log.Infof("User %s completed purchase of $%.2f", userID, amount)
   ```

2. **Include Request IDs**
   ```go
   log.WithFields(
       zap.String(logger.RequestIDKey, requestID),
   ).Info("Handling request")
   ```

3. **Proper Error Logging**
   ```go
   if err != nil {
       log.WithFields(
           zap.Error(err),
           zap.String("operation", "database_query"),
       ).Error("Query failed")
   }
   ```

4. **Resource Cleanup**
   ```go
   defer logger.Sync()
   
   // For OpenTelemetry integration
   defer func() {
       ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
       defer cancel()
       logger.ShutdownOpenTelemetryLogger(ctx)
   }()
   ```

5. **OpenTelemetry Best Practices**
   - Always set a meaningful `ServiceName` for better observability
   - Use appropriate protocol (`http` vs `grpc`) based on your infrastructure
   - Include authentication headers when required by your observability platform
   - Handle shutdown gracefully to ensure all logs are flushed
   - Test connectivity to your OpenTelemetry endpoint before production deployment

## Configuration Examples

### Development Configuration

```yaml
# config.yaml
logger:
  level: "debug"
  encoding: "console"
  opentelemetry:
    enabled: true
    service_name: "my-service-dev"
    endpoint: "http://localhost:4318"
    protocol: "http"
    insecure: true
```

### Production Configuration

```yaml
# config.yaml
logger:
  level: "info"
  encoding: "json"
  opentelemetry:
    enabled: true
    service_name: "my-service-prod"
    endpoint: "https://otel-collector.company.com:4318"
    protocol: "http"
    insecure: false
    headers:
      authorization: "Bearer your-token-here"
      x-api-key: "your-api-key-here"
```

## Performance Considerations

- The logger is designed to be zero-allocation in most cases
- JSON encoding is more CPU-intensive but provides structured data
- Log level checks are performed atomically
- Field allocation is optimized for minimal overhead
- OpenTelemetry integration adds minimal overhead when properly configured
- Batch processing is used for OpenTelemetry exports to optimize performance

## Thread Safety

The logger is completely thread-safe and can be used concurrently from multiple goroutines. The OpenTelemetry integration maintains thread safety through proper synchronization mechanisms.
