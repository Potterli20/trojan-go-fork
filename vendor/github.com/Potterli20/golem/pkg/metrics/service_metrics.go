package metrics

import (
	"github.com/Potterli20/golem/pkg/logger"
	"github.com/Potterli20/golem/pkg/metrics/collectors"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/net"
	"runtime"
	"runtime/pprof"
	"time"
)

const (
	memoryUsageBytes  = "memory_usage_bytes"
	cpuUsagePercent   = "cpu_usage_percent"
	bytesSent         = "bytes_sent"
	bytesReceived     = "bytes_received"
	activeConnections = "system_active_connections"
	goroutinesCount   = "goroutines_count"
	threadsCount      = "threads_count"
)

func RegisterSystemMetrics(metricsServer TaskMetrics) []error {
	var errs []error

	register := func(name, help string, labels []string, handler MetricHandler) {
		if err := metricsServer.RegisterMetric(name, help, labels, handler); err != nil {
			errs = append(errs, err)
		}
	}

	register(memoryUsageBytes, "Memory usage in bytes.", nil, &collectors.Gauge{})
	register(cpuUsagePercent, "CPU usage in percent.", nil, &collectors.Gauge{})
	register(bytesSent, "Total bytes sent over network.", nil, &collectors.Counter{})
	register(bytesReceived, "Total bytes received over network.", nil, &collectors.Counter{})
	register(activeConnections, "Number of active network connections.", nil, &collectors.Gauge{})
	register(goroutinesCount, "Number of goroutines.", nil, &collectors.Gauge{})
	register(threadsCount, "Number of OS threads.", nil, &collectors.Gauge{})

	return errs
}

func UpdateSystemMetrics(metricsServer TaskMetrics, updateInterval time.Duration) {
	for {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		if err := metricsServer.UpdateMetric(memoryUsageBytes, float64(m.Alloc)); err != nil {
			logger.Errorf("error updating %v: %v", memoryUsageBytes, err)
		}

		cpuPercents, _ := cpu.Percent(time.Second, false)
		if err := metricsServer.UpdateMetric(cpuUsagePercent, cpuPercents[0]); err != nil {
			logger.Errorf("error updating %v: %v", cpuUsagePercent, err)
		}

		netIO, _ := net.IOCounters(false)
		if err := metricsServer.UpdateMetric(bytesSent, float64(netIO[0].BytesSent)); err != nil {
			logger.Errorf("error updating %v: %v", bytesSent, err)
		}
		if err := metricsServer.UpdateMetric(bytesReceived, float64(netIO[0].BytesRecv)); err != nil {
			logger.Errorf("error updating %v: %v", bytesReceived, err)
		}

		conns, _ := net.Connections("all")
		if err := metricsServer.UpdateMetric(activeConnections, float64(len(conns))); err != nil {
			logger.Errorf("error updating %v: %v", activeConnections, err)
		}

		if err := metricsServer.UpdateMetric(goroutinesCount, float64(runtime.NumGoroutine())); err != nil {
			logger.Errorf("error updating %v: %v", goroutinesCount, err)
		}

		threads := pprof.Lookup("threadcreate").Count()
		if err := metricsServer.UpdateMetric(threadsCount, float64(threads)); err != nil {
			logger.Errorf("error updating %v: %v", threadsCount, err)
		}

		time.Sleep(updateInterval)
	}
}
