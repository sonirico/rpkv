// Package topicshape watches a topic's partition count and cleanup policy
// through the Kafka admin API, so drift is observable over healthz and
// metrics without a restart.
package topicshape

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"

	"github.com/sonirico/rpkv/clock"
	"github.com/sonirico/rpkv/server"
)

// topicLister lists a topic's partition details; satisfied by
// *kadm.Client.
type topicLister interface {
	ListTopics(ctx context.Context, topics ...string) (kadm.TopicDetails, error)
}

// configDescriber describes a topic's configuration, including
// cleanup.policy; satisfied by *kadm.Client.
type configDescriber interface {
	DescribeTopicConfigs(ctx context.Context, topics ...string) (kadm.ResourceConfigs, error)
}

// Watcher periodically reads a topic's partition count and cleanup policy
// through the Kafka admin API and caches the last observed shape.
type Watcher struct {
	lister    topicLister
	describer configDescriber
	topic     string
	clk       clock.Clock
	interval  time.Duration
	logger    *slog.Logger

	mu     sync.RWMutex
	shape  server.TopicShape
	primed bool
}

// NewWatcher wires an already-configured topic lister, config describer,
// clock and interval into a Watcher for topic.
func NewWatcher(
	l topicLister,
	d configDescriber,
	topic string,
	clk clock.Clock,
	interval time.Duration,
	logger *slog.Logger,
) *Watcher {
	return &Watcher{
		lister:    l,
		describer: d,
		topic:     topic,
		clk:       clk,
		interval:  interval,
		logger:    logger,
	}
}

// Refresh reads the topic's current partition count and cleanup.policy
// config value and stores them. A transition in either field from the
// previously stored shape is logged once at WARN, compared before the new
// shape overwrites the stored one.
func (w *Watcher) Refresh(ctx context.Context) error {
	topics, err := w.lister.ListTopics(ctx, w.topic)
	if err != nil {
		return fmt.Errorf("topicshape: list topics %q: %w", w.topic, err)
	}
	detail, ok := topics[w.topic]
	if !ok {
		return fmt.Errorf("topicshape: topic %q not found", w.topic)
	}
	if detail.Err != nil {
		return fmt.Errorf("topicshape: topic %q: %w", w.topic, detail.Err)
	}

	configs, err := w.describer.DescribeTopicConfigs(ctx, w.topic)
	if err != nil {
		return fmt.Errorf("topicshape: describe configs %q: %w", w.topic, err)
	}
	resource, err := configs.On(w.topic, nil)
	if err != nil {
		return fmt.Errorf("topicshape: configs %q: %w", w.topic, err)
	}

	var cleanupPolicy string
	for _, c := range resource.Configs {
		if c.Key == "cleanup.policy" {
			cleanupPolicy = c.MaybeValue()
			break
		}
	}

	next := server.TopicShape{
		PartitionCount: int32(len(detail.Partitions)),
		CleanupPolicy:  cleanupPolicy,
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.primed && (next.PartitionCount != w.shape.PartitionCount ||
		next.CleanupPolicy != w.shape.CleanupPolicy) {
		w.logger.Warn(
			"topicshape: topic shape changed",
			"topic", w.topic,
			"old_partition_count", w.shape.PartitionCount,
			"new_partition_count", next.PartitionCount,
			"old_cleanup_policy", w.shape.CleanupPolicy,
			"new_cleanup_policy", next.CleanupPolicy,
		)
	}

	w.shape = next
	w.primed = true

	return nil
}

// RunLoop calls Refresh every interval until ctx is done. A failed refresh
// is logged and does not stop the loop.
func (w *Watcher) RunLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.clk.After(w.interval):
		}

		if err := w.Refresh(ctx); err != nil {
			w.logger.Error("topicshape: refresh", "topic", w.topic, "error", err)
		}
	}
}

// Shape returns the last refreshed topic shape. Safe for concurrent use.
func (w *Watcher) Shape() server.TopicShape {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.shape
}
