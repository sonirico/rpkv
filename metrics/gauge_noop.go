package metrics

var _ Gauge = noopGauge{}

// noopGauge is a Gauge that discards every observation.
type noopGauge struct{}

// NewNoopGauge returns a Gauge that discards every observation.
func NewNoopGauge() Gauge {
	return noopGauge{}
}

func (noopGauge) Set(v float64) {}
