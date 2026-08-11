// Package offsets provides a kadm-backed per-topic log-end offset source
// for the server's healthz.
package offsets

import (
	"context"
	"fmt"

	"github.com/twmb/franz-go/pkg/kadm"
)

// Source lists a topic's per-partition log-end offsets through the Kafka
// admin API; it structurally satisfies server's offsetSource.
type Source struct {
	admin *kadm.Client
	topic string
}

// NewSource wires an already-built kadm client into a Source for topic.
func NewSource(admin *kadm.Client, topic string) *Source {
	return &Source{admin: admin, topic: topic}
}

// LogEndOffsets returns partition -> log-end offset for the topic.
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
	return ends, nil
}
