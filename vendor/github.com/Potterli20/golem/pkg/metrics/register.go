package metrics

import (
	"fmt"
	"github.com/Potterli20/golem/pkg/metrics/collectors"
	"github.com/prometheus/client_golang/prometheus"
	"regexp"
	"strings"
)

const (
	APPNameLabel = "app_name"
)

type collectorRegister func(name, help string, labels []string, handler MetricHandler) (prometheus.Collector, error)

var (
	collectorRegisterMap = map[string]collectorRegister{
		collectors.CounterType:   registerCounter,
		collectors.HistogramType: registerHistogram,
		collectors.GaugeType:     registerGauge,
	}
)

func (t *taskMetrics) RegisterMetric(name string, help string, labels []string, handler MetricHandler) error {
	if handler == nil {
		panic("handler is mandatory")
	}

	labels = append(labels, APPNameLabel)
	var metric prometheus.Collector

	name = formatMetricName(name)
	registerFn, ok := collectorRegisterMap[handler.Type()]
	if !ok {
		return fmt.Errorf("unsupported metric type")
	}

	metric, err := registerFn(name, help, labels, handler)
	if err != nil {
		return err
	}

	if err = prometheus.Register(metric); err != nil {
		return err
	}

	t.mux.Lock()
	defer t.mux.Unlock()
	t.metrics[name] = MetricDetail{Collector: metric, Handler: handler}

	return nil
}

func registerCounter(name, help string, labels []string, handler MetricHandler) (prometheus.Collector, error) {
	if _, ok := handler.(*collectors.Counter); !ok {
		return nil, fmt.Errorf("invalid handler type for counter")
	}

	if len(labels) == 0 {
		return prometheus.NewCounter(prometheus.CounterOpts{Name: name, Help: help}), nil
	}

	return prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labels), nil
}

func registerHistogram(name, help string, labels []string, handler MetricHandler) (prometheus.Collector, error) {
	if histogramUpdater, ok := handler.(*collectors.Histogram); ok {
		if len(labels) == 0 {
			return prometheus.NewHistogram(prometheus.HistogramOpts{Name: name, Help: help, Buckets: histogramUpdater.Buckets}), nil
		}
		return prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: name, Help: help, Buckets: histogramUpdater.Buckets}, labels), nil
	}

	return nil, fmt.Errorf("invalid handler type for histogram")
}

func registerGauge(name, help string, labels []string, handler MetricHandler) (prometheus.Collector, error) {
	if _, ok := handler.(*collectors.Gauge); !ok {
		return nil, fmt.Errorf("invalid handler type for gauge")
	}

	if len(labels) == 0 {
		return prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help}), nil
	}

	return prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name, Help: help}, labels), nil
}

// FormatMetricName formats the given string to make it a valid Prometheus metric name.
func formatMetricName(name string) string {
	name = strings.ReplaceAll(name, "-", "_")
	re := regexp.MustCompile("[^a-zA-Z0-9_]+")
	name = re.ReplaceAllString(name, "_")

	return name
}
