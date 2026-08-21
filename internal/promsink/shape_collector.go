package promsink

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sonirico/rpkv/server"
)

// shapeSource reports a topic's last observed partition count and
// cleanup policy; satisfied by *topicshape.Watcher.
type shapeSource interface {
	Shape() server.TopicShape
}

var topicPartitionsDesc = prometheus.NewDesc(
	"rpkv_topic_partitions",
	"Partition count of the topic, as last observed.",
	[]string{"topic"},
	nil,
)

var topicCompactedDesc = prometheus.NewDesc(
	"rpkv_topic_compacted",
	"1 if the topic's cleanup.policy contains compact, else 0.",
	[]string{"topic"},
	nil,
)

// shapeCollector reports rpkv_topic_partitions and rpkv_topic_compacted at
// scrape time from the topic's last observed shape, rather than being
// updated from the ingest hot path.
type shapeCollector struct {
	topic string
	src   shapeSource
}

var _ prometheus.Collector = (*shapeCollector)(nil)

// newShapeCollector wires an already-built shape source into a
// shapeCollector for topic.
func newShapeCollector(topic string, src shapeSource) *shapeCollector {
	return &shapeCollector{topic: topic, src: src}
}

// Describe sends topicPartitionsDesc and topicCompactedDesc, the
// collector's two metric descriptors.
func (c *shapeCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- topicPartitionsDesc
	ch <- topicCompactedDesc
}

// Collect emits the topic's current partition count and whether its
// cleanup policy contains "compact".
func (c *shapeCollector) Collect(ch chan<- prometheus.Metric) {
	shape := c.src.Shape()

	ch <- prometheus.MustNewConstMetric(
		topicPartitionsDesc,
		prometheus.GaugeValue,
		float64(shape.PartitionCount),
		c.topic,
	)

	compacted := 0.0
	if strings.Contains(shape.CleanupPolicy, "compact") {
		compacted = 1.0
	}
	ch <- prometheus.MustNewConstMetric(
		topicCompactedDesc,
		prometheus.GaugeValue,
		compacted,
		c.topic,
	)
}
