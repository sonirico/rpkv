package controller

import "context"

// healthSource reads a topic's runtime shape from an rpkv process's
// /healthz endpoint.
type healthSource interface {
	Shape(ctx context.Context, baseURL, topic string) (topicShape, error)
}

// topicShape is the per-topic shape reported by /healthz.
type topicShape struct {
	CleanupPolicy  string
	PartitionCount int32
}
