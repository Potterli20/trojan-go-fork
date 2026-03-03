package collectors

import (
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
)

type Histogram struct{ Buckets []float64 }

func (h *Histogram) Update(collector prometheus.Collector, value float64, labels ...string) error {
	if len(labels) > 0 {
		if metricVec, ok := collector.(*prometheus.HistogramVec); ok {
			metric := metricVec.WithLabelValues(labels...)
			metric.Observe(value)
			return nil
		}
		return fmt.Errorf("invalid metric type, expected HistogramVec for labels")
	}

	if metric, ok := collector.(prometheus.Histogram); ok {
		metric.Observe(value)
		return nil
	}

	return fmt.Errorf("invalid metric type expected histogram")
}
