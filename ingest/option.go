package ingest

// Option configures an Ingester at construction time.
type Option func(*Ingester)

// WithMetrics sets the non-nil fields of m onto the Ingester's metrics,
// leaving noops for any field left unset.
func WithMetrics(m Metrics) Option {
	return func(in *Ingester) {
		if m.ApplyBatchSize != nil {
			in.metrics.ApplyBatchSize = m.ApplyBatchSize
		}
		if m.NullKeysSkipped != nil {
			in.metrics.NullKeysSkipped = m.NullKeysSkipped
		}
	}
}
