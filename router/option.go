package router

// Option configures a Router at construction time.
type Option func(*Router)

// WithMetrics sets the non-nil fields of m onto the Router's metrics,
// leaving noops for any field left unset.
func WithMetrics(m Metrics) Option {
	return func(s *Router) {
		if m.AmbiguousKeys != nil {
			s.metrics.AmbiguousKeys = m.AmbiguousKeys
		}
	}
}
