package server

// Option configures a Server at construction time.
type Option func(*Server)

// WithMetrics sets the non-nil fields of m onto the Server's metrics,
// leaving noops for any field left unset.
func WithMetrics(m Metrics) Option {
	return func(s *Server) {
		if m.RequestDuration != nil {
			s.metrics.RequestDuration = m.RequestDuration
		}
		if m.SupersedeRetries != nil {
			s.metrics.SupersedeRetries = m.SupersedeRetries
		}
	}
}
