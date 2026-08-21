// Package offsets provides a kadm-backed per-topic log-end offset source
// for the server's healthz.
package offsets

import (
	"context"
	"fmt"

	"github.com/twmb/franz-go/pkg/kadm"

	"github.com/sonirico/rpkv/index"
)

// Source lists a topic's per-partition log-end offsets through the Kafka
// admin API; it structurally satisfies server's offsetSource.
type Source struct {
	admin *kadm.Client
	topic string
	owned index.Ownership
}

// NewSource wires an already-built kadm client into a Source for topic,
// restricted to the given partition ownership.
func NewSource(admin *kadm.Client, topic string, owned index.Ownership) *Source {
	return &Source{admin: admin, topic: topic, owned: owned}
}

// LogEndOffsets returns partition -> log-end offset for the topic,
// restricted to the owned partitions.
func (s *Source) LogEndOffsets(ctx context.Context) (map[int32]int64, error) {
	listed, err := s.admin.ListEndOffsets(ctx, s.topic)
	if err != nil {
		return nil, fmt.Errorf("offsets: list end offsets %q: %w", s.topic, err)
	}
	if err := listed.Error(); err != nil {
		return nil, fmt.Errorf("offsets: list end offsets %q: %w", s.topic, err)
	}

	ends := make(map[int32]int64)
	listed.Each(func(lo kadm.ListedOffset) {
		ends[lo.Partition] = lo.Offset
	})
	for p := range ends {
		if !s.owned.Owns(p) {
			delete(ends, p)
		}
	}
	return ends, nil
}
