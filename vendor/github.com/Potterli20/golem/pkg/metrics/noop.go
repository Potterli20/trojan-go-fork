package metrics

var _ TaskMetrics = NoopMetrics{}

// NoopMetrics provides an implementation of the TaskMetrics that produces no telemetry and minimizes used computation resources.
type NoopMetrics struct{}

// NewNoopMetrics creates and returns a new instance that implements the TaskMetrics interface without generating any metrics or telemetry.
func NewNoopMetrics() TaskMetrics {
	return &NoopMetrics{}
}

func (n NoopMetrics) Start() error {
	return nil
}

func (n NoopMetrics) RegisterMetric(_, _ string, _ []string, _ MetricHandler) error {
	return nil
}

func (n NoopMetrics) UpdateMetric(_ string, _ float64, _ ...string) error {
	return nil
}

func (n NoopMetrics) IncrementMetric(_ string, _ ...string) error {
	return nil
}

func (n NoopMetrics) DecrementMetric(_ string, _ ...string) error {
	return nil
}

func (n NoopMetrics) Name() string {
	return ""
}

func (n NoopMetrics) AppName() string {
	return ""
}

func (n NoopMetrics) Stop() error {
	return nil
}
