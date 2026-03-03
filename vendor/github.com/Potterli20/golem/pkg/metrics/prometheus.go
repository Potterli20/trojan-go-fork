package metrics

import (
	"fmt"
	"github.com/Potterli20/golem/pkg/logger"
	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
	"sync"
	"time"
)

// StartMetricsServer starts a prometheus server.
// Data Url is at localhost:<port>/metrics/<path>
// Normally you would use /metrics as endpoint and 9090 as port

type TaskMetrics interface {
	Start() error
	RegisterMetric(name string, help string, labels []string, handler MetricHandler) error
	UpdateMetric(name string, value float64, labels ...string) error
	IncrementMetric(name string, labels ...string) error
	DecrementMetric(name string, labels ...string) error
	Name() string
	AppName() string
	Stop() error
}

type MetricDetail struct {
	Collector prometheus.Collector
	Handler   MetricHandler
}

type taskMetrics struct {
	path    string
	port    string
	metrics map[string]MetricDetail
	mux     sync.RWMutex
	appName string
}

func NewTaskMetrics(path, port, appName string) TaskMetrics {
	if appName == "" {
		panic("appName is mandatory")
	}

	return &taskMetrics{
		path:    path,
		port:    port,
		appName: appName,
		metrics: make(map[string]MetricDetail),
	}
}

func (t *taskMetrics) Name() string {
	return "metrics"
}

func (t *taskMetrics) AppName() string {
	return t.appName
}

func (t *taskMetrics) Start() error {
	router := chi.NewRouter()

	logger.Infof("Metrics (prometheus) starting: %v", t.port)

	// Prometheus path
	router.Get(t.path, promhttp.Handler().(http.HandlerFunc))

	server := &http.Server{
		Addr:              fmt.Sprintf(":%s", t.port),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	err := server.ListenAndServe()
	if err != nil {
		logger.Errorf("Prometheus server error: %v", err)
		return err
	}

	logger.Infof("Prometheus server serving at port %s", t.port)

	return nil
}

func (t *taskMetrics) Stop() error {
	return nil
}
