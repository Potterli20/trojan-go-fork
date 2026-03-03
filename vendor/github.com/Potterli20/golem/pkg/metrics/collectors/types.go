package collectors

const (
	CounterType   = "counter"
	HistogramType = "histogram"
	GaugeType     = "gauge"
)

func (c *Counter) Type() string {
	return CounterType
}

func (h *Histogram) Type() string {
	return HistogramType
}

func (g *Gauge) Type() string {
	return GaugeType
}
