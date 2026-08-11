package metricstest

import (
	"sync"

	"github.com/sonirico/rpkv/metrics"
)

var _ metrics.Gauge = (*Gauge)(nil)

// Gauge is a metrics.Gauge that records its last set value, safe for
// concurrent use.
type Gauge struct {
	mu    sync.Mutex
	value float64
}

// NewGauge returns a Gauge starting at 0.
func NewGauge() *Gauge {
	return &Gauge{}
}

// Set sets the gauge to v.
func (g *Gauge) Set(v float64) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.value = v
}

// Value returns the last value set on the gauge.
func (g *Gauge) Value() float64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.value
}
