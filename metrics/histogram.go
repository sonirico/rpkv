package metrics

// Histogram samples observations into buckets, structurally matching
// Prometheus's histogram contract.
type Histogram interface {
	// Observe records v as an observation.
	Observe(v float64)
}
