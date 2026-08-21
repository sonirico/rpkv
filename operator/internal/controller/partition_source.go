package controller

import "context"

// partitionSource reads a topic's current partition count.
type partitionSource interface {
	Partitions(ctx context.Context, topic string) (int32, error)
}
