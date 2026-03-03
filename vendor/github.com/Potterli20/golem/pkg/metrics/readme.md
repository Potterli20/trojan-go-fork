# Task Metrics using Prometheus in Go

## Table of Contents

- [Introduction](#introduction)
- [Features](#features)
- [Default Labels](#default-labels)
- [Code Structure](#code-structure)
- [Metrics Collectors](#metrics-collectors)
  - [Counter](#counter)
  - [Gauge](#gauge)
  - [Histogram](#histogram)
- [Usage](#usage)
  - [Starting the Server](#starting-the-server)
  - [Registering Metrics](#registering-metrics)
  - [Updating Metrics](#updating-metrics)

## Introduction

This project provides a comprehensive metrics collection and reporting framework integrated with Prometheus for Go applications.

## Features

- Different types of metrics collectors including Counter, Gauge, and Histogram.
- Built-in Prometheus server.
- Strong typing to prevent metrics misuse.
- Error handling and logging integrated with Uber's Zap library.
- Support for custom labels.
- Thread-safe metric updates with read-write mutexes.
- Auto-registering of metrics based on type.

## Default Labels

Every metric reported by the framework automatically includes the following labels to provide more context about the application's environment:

- `app_name`: Identifies the name of the application.
- `app_revision`: Specifies the current revision of the application, typically a Git commit hash or a similar identifier.
- `app_version`: Indicates the current version of the application.

These labels help in distinguishing metrics across different environments, versions, and revisions of the application. For example, a metric for request duration might look like this:

```
request_duration_ms{app_name="ledger-live",app_revision="main-9a60456",app_version="v0.8.3",method="GET",path="/addresses/{address}/transactions",status="200"} 435
```

This approach ensures that metrics data can be correlated with specific releases and deployments of the application, providing valuable insights during analysis and troubleshooting.

## Code Structure

- `/metrics/collectors/`: Contains the implementation of various metrics collectors (Counter, Gauge, Histogram).
- `/metrics/handler.go`: Contains the MetricHandler interface and taskMetrics `UpdateMetric`, `IncrementMetric`, and `DecrementMetric` methods for updating metrics.
- `/metrics/prometheus.go`: Defines the Prometheus server.
- `/metrics/register.go`: Responsible for registering metrics with the Prometheus server.

## Metrics Collectors

### Counter

- **File**: `/metrics/collectors/counter.go`
- **Methods**: `Update`
- **Usage**: Counters are cumulative metrics that can only increase.

### Gauge

- **File**: `/metrics/collectors/gauge.go`
- **Methods**: `Update`, `Inc`, `Dec`
- **Usage**: Gauges are metrics that can arbitrarily go up and down.

### Histogram

- **File**: `/metrics/collectors/histogram.go`
- **Methods**: `Update`
- **Usage**: Histograms count observations (like request durations or response sizes) and place them in configurable buckets.

## Usage

### Starting the Server

```go
metricsServer := metrics.NewTaskMetrics("/metrics", "9090")
err := metricsServer.Start()
if err != nil {
    log.Fatal(err)
}
```

### Registering Metrics

```go
// Without labels
err = metricsServer.RegisterMetric("my_counter", "This is a counter metric", nil, &collectors.Counter{})

// With labels
err = metricsServer.RegisterMetric("my_counter_with_labels", "This is a counter metric with labels", []string{"label1", "label2"}, &collectors.Counter{})

// Register a gauge
err = metricsServer.RegisterMetric("my_gauge", "This is a gauge metric", nil, &collectors.Gauge{})
```

### Updating Metrics

```go
// Without labels
metricsServer.UpdateMetric("my_counter", 1)

// With labels
metricsServer.UpdateMetric("my_counter_with_labels", 1, "label1_value", "label2_value")

// Increment a gauge
metricsServer.IncrementMetric("my_gauge")

// Decrement a gauge
metricsServer.DecrementMetric("my_gauge")
```

### Mocking Support

Use MockTaskMetrics for testing.

```go
func TestMetrics(t *testing.T) {
    tm := &metrics.MockTaskMetrics{}
	  tm.On("RegisterMetric", "local_cache_cleanup_errors", mock.Anything, []string{"error_type"}, mock.Anything).Once().Return(nil)
    // Use tm in your tests
}

```

To generate mocks:

- install: https://github.com/vektra/mockery
- pull the repo with the correct version of the interface you want to mock
- ` mockery --name TaskMetrics --dir ./pkg/metrics --output . --filename task_metrics_mock.go --structname MockTaskMetrics --inpackage`
- For usage in tests

