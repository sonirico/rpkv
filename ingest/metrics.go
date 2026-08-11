package ingest

import "github.com/sonirico/rpkv/metrics"

// Metrics holds the counters and histograms an Ingester reports through,
// per applied batch.
type Metrics struct {
	ApplyBatchSize  metrics.Histogram
	NullKeysSkipped metrics.Counter
}
