// Package metricstest provides recording fakes of the metrics facade for
// use in tests that assert on emitted metrics.
package metricstest

import (
	"sync"

	"github.com/sonirico/rpkv/metrics"
)

var _ metrics.Counter = (*Counter)(nil)

// Counter is a metrics.Counter that records its accumulated value, safe for
// concurrent use.
type Counter struct {
	mu    sync.Mutex
	count float64
}

// NewCounter returns a Counter starting at 0.
func NewCounter() *Counter {
	return &Counter{}
}

// Inc increments the counter by 1.
func (c *Counter) Inc() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.count++
}

// Add increments the counter by delta.
func (c *Counter) Add(delta float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.count += delta
}

// Count returns the counter's accumulated value.
func (c *Counter) Count() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.count
}
