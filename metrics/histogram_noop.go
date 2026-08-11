package metrics

var _ Histogram = noopHistogram{}

// noopHistogram is a Histogram that discards every observation.
type noopHistogram struct{}

// NewNoopHistogram returns a Histogram that discards every observation.
func NewNoopHistogram() Histogram {
	return noopHistogram{}
}

func (noopHistogram) Observe(v float64) {}
