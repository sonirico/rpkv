package controller

import "context"

// PartitionSource reads a topic's current partition count. Exported so
// cmd/main.go's wiring can name it when building a PartitionSourceFactory
// closure; nothing else outside this package should depend on it.
type PartitionSource interface {
	Partitions(ctx context.Context, topic string) (int32, error)
}
