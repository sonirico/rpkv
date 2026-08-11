package metricstest

import (
	"sync"

	"github.com/sonirico/rpkv/metrics"
)

var _ metrics.Histogram = (*Histogram)(nil)

// Histogram is a metrics.Histogram that records every observation in
// order, safe for concurrent use.
type Histogram struct {
	mu           sync.Mutex
	observations []float64
}

// NewHistogram returns a Histogram with no observations.
func NewHistogram() *Histogram {
	return &Histogram{}
}

// Observe records v as an observation.
func (h *Histogram) Observe(v float64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.observations = append(h.observations, v)
}

// Observations returns a copy of every observation recorded so far, in the
// order Observe was called.
func (h *Histogram) Observations() []float64 {
	h.mu.Lock()
	defer h.mu.Unlock()

	out := make([]float64, len(h.observations))
	copy(out, h.observations)
	return out
}
