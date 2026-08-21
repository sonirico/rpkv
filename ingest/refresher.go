package ingest

import (
	"context"
	"log/slog"
	"time"

	"github.com/sonirico/rpkv/clock"
)

// partitionSyncer re-discovers a topic's partition set and assigns any
// newly-seen partition for consumption; satisfied by *Ingester.
type partitionSyncer interface {
	SyncPartitions(ctx context.Context) error
}

// Refresher periodically calls SyncPartitions on a syncer so ingest picks
// up partitions added to a topic after Run started, without a restart.
type Refresher struct {
	syncer   partitionSyncer
	clk      clock.Clock
	interval time.Duration
	logger   *slog.Logger
}

// NewRefresher wires an already-configured syncer, clock and interval into
// a Refresher.
func NewRefresher(
	s partitionSyncer,
	clk clock.Clock,
	interval time.Duration,
	logger *slog.Logger,
) *Refresher {
	return &Refresher{syncer: s, clk: clk, interval: interval, logger: logger}
}

// RunLoop calls SyncPartitions every interval until ctx is done. A failed
// refresh is logged and does not stop the loop or kill ingest.
func (r *Refresher) RunLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.clk.After(r.interval):
		}

		if err := r.syncer.SyncPartitions(ctx); err != nil {
			r.logger.Error("ingest: refresh partitions", "error", err)
		}
	}
}
