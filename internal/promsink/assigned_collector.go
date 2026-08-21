package promsink

import (
	"github.com/prometheus/client_golang/prometheus"
)

// assignedPartitionsSource reports how many partitions are currently
// assigned for consumption; satisfied by *ingest.Ingester.
type assignedPartitionsSource interface {
	AssignedPartitions() int
}

var assignedPartitionsDesc = prometheus.NewDesc(
	"rpkv_ingest_partitions_assigned",
	"Partitions currently assigned for consumption, by topic.",
	[]string{"topic"},
	nil,
)

// assignedCollector reports rpkv_ingest_partitions_assigned at scrape
// time from the ingester's own assignment state, rather than being
// updated from the ingest hot path.
type assignedCollector struct {
	topic string
	src   assignedPartitionsSource
}

var _ prometheus.Collector = (*assignedCollector)(nil)

// newAssignedCollector wires an already-built assigned-partitions source
// into an assignedCollector for topic.
func newAssignedCollector(topic string, src assignedPartitionsSource) *assignedCollector {
	return &assignedCollector{topic: topic, src: src}
}

// Describe sends assignedPartitionsDesc, the collector's single metric
// descriptor.
func (c *assignedCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- assignedPartitionsDesc
}

// Collect emits the topic's currently assigned partition count.
func (c *assignedCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(
		assignedPartitionsDesc,
		prometheus.GaugeValue,
		float64(c.src.AssignedPartitions()),
		c.topic,
	)
}
