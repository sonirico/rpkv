package server

import "github.com/sonirico/rpkv/metrics"

// Metrics holds the counters and histograms a Server reports through, per
// request handled.
type Metrics struct {
	RequestDuration  metrics.Histogram
	SupersedeRetries metrics.Counter
}
