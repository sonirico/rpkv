package ingest

import "github.com/sonirico/rpkv/index"

// Option configures an Ingester at construction time.
type Option func(*Ingester)

// WithPartitions restricts the ingester to the given partition set; the
// default is owning every partition.
func WithPartitions(o index.Ownership) Option {
	return func(in *Ingester) {
		in.owned = o
	}
}

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
