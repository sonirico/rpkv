package router

import "github.com/sonirico/rpkv/metrics"

// Metrics holds the counters a Router reports through, per request
// handled.
type Metrics struct {
	// AmbiguousKeys counts requests where more than one shard answered
	// 200 for the same key.
	AmbiguousKeys metrics.Counter
}
