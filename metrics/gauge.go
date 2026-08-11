package metrics

// Gauge is a value that can go up or down, structurally matching
// Prometheus's gauge contract.
type Gauge interface {
	// Set sets the gauge to v.
	Set(v float64)
}
