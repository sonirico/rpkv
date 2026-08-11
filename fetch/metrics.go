package fetch

import "github.com/sonirico/rpkv/metrics"

// Metrics holds the counters a Fetcher reports through, per outcome of
// FetchAt.
type Metrics struct {
	Hits       metrics.Counter
	Superseded metrics.Counter
	Evicted    metrics.Counter
	Errors     metrics.Counter
}
