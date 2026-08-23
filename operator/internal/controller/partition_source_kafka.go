package controller

import (
	"context"
	"fmt"

	"github.com/twmb/franz-go/pkg/kadm"
)

// kafkaPartitionSource reads a topic's partition count over the Kafka
// admin protocol.
type kafkaPartitionSource struct {
	admin *kadm.Client
}

// NewKafkaPartitionSource wires an already-configured kadm client into a
// kafkaPartitionSource. Exported so cmd/main.go's wiring can build one
// per RpkvIndex's seed brokers.
func NewKafkaPartitionSource(admin *kadm.Client) *kafkaPartitionSource {
	return &kafkaPartitionSource{admin: admin}
}

// Partitions lists the topic's metadata and counts its partitions.
func (s *kafkaPartitionSource) Partitions(ctx context.Context, topic string) (int32, error) {
	topics, err := s.admin.ListTopics(ctx, topic)
	if err != nil {
		return 0, fmt.Errorf("kafkaPartitionSource: list topics %q: %w", topic, err)
	}

	detail, ok := topics[topic]
	if !ok {
		return 0, fmt.Errorf("kafkaPartitionSource: topic %q not found", topic)
	}
	if detail.Err != nil {
		return 0, fmt.Errorf("kafkaPartitionSource: topic %q: %w", topic, detail.Err)
	}

	return int32(len(detail.Partitions)), nil
}
