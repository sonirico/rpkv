package fetch

// Option configures a Fetcher at construction time.
type Option func(*Fetcher)

// WithMetrics sets the non-nil fields of m onto the Fetcher's metrics,
// leaving noops for any field left unset.
func WithMetrics(m Metrics) Option {
	return func(f *Fetcher) {
		if m.Hits != nil {
			f.metrics.Hits = m.Hits
		}
		if m.Superseded != nil {
			f.metrics.Superseded = m.Superseded
		}
		if m.Evicted != nil {
			f.metrics.Evicted = m.Evicted
		}
		if m.Errors != nil {
			f.metrics.Errors = m.Errors
		}
	}
}
