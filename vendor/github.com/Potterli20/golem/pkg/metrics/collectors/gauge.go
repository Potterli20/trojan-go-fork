package collectors

import (
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
)

type Gauge struct{}

func (g *Gauge) Update(collector prometheus.Collector, value float64, labels ...string) error {
	if len(labels) > 0 {
		if metricVec, ok := collector.(*prometheus.GaugeVec); ok {
			metric := metricVec.WithLabelValues(labels...)
			metric.Set(value)
			return nil
		}
		return fmt.Errorf("invalid metric type, expected GaugeVec for labels")
	}

	if metric, ok := collector.(prometheus.Gauge); ok {
		metric.Set(value)
		return nil
	}

	return fmt.Errorf("invalid metric type, expected Gauge")
}

func (g *Gauge) Inc(collector prometheus.Collector, labels ...string) error {
	if len(labels) > 0 {
		if metricVec, ok := collector.(*prometheus.GaugeVec); ok {
			metric := metricVec.WithLabelValues(labels...)
			metric.Inc()
			return nil
		}
		return fmt.Errorf("invalid metric type, expected GaugeVec for labels")
	}

	if metric, ok := collector.(prometheus.Gauge); ok {
		metric.Inc()
		return nil
	}

	return fmt.Errorf("invalid metric type, expected Gauge")
}

func (g *Gauge) Dec(collector prometheus.Collector, labels ...string) error {
	if len(labels) > 0 {
		if metricVec, ok := collector.(*prometheus.GaugeVec); ok {
			metric := metricVec.WithLabelValues(labels...)
			metric.Dec()
			return nil
		}
		return fmt.Errorf("invalid metric type, expected GaugeVec for labels")
	}

	if metric, ok := collector.(prometheus.Gauge); ok {
		metric.Dec()
		return nil
	}

	return fmt.Errorf("invalid metric type, expected Gauge")
}
