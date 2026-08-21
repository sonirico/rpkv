package server

// TopicShape is a topic's partition count and cleanup policy, as last
// observed by a shapeSource.
type TopicShape struct {
	PartitionCount int32
	CleanupPolicy  string
}

// shapeSource provides a topic's last observed partition count and
// cleanup policy, used to enrich healthz.
type shapeSource interface {
	Shape() TopicShape
}
