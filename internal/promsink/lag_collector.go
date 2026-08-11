package promsink

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// checkpointReader reads a partition's highest applied offset; satisfied
// by *index.Index.
type checkpointReader interface {
	Checkpoint(partition int32) (int64, error)
}

// logEndSource lists a topic's per-partition log-end offsets; satisfied by
// *offsets.Source.
type logEndSource interface {
	LogEndOffsets(ctx context.Context) (map[int32]int64, error)
}

var lagDesc = prometheus.NewDesc(
	"rpkv_ingest_lag",
	"Records between the applied checkpoint and the log end, by topic and partition.",
	[]string{"topic", "partition"},
	nil,
)

// lagCollector computes rpkv_ingest_lag at scrape time from the same
// checkpoint and log-end-offset data the healthz endpoint reports, rather
// than being updated from the ingest hot path.
type lagCollector struct {
	topic  string
	cp     checkpointReader
	src    logEndSource
	logger *slog.Logger
}

var _ prometheus.Collector = (*lagCollector)(nil)

// newLagCollector wires an already-built checkpoint reader and log-end
// source into a lagCollector for topic.
func newLagCollector(
	topic string,
	cp checkpointReader,
	src logEndSource,
	logger *slog.Logger,
) *lagCollector {
	return &lagCollector{topic: topic, cp: cp, src: src, logger: logger}
}

// Describe sends lagDesc, the collector's single metric descriptor.
func (c *lagCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- lagDesc
}

// Collect fetches the topic's current log-end offsets and emits
// max(0, logEnd-checkpoint-1) per partition; a partition whose checkpoint
// or log-end read fails is skipped, not failed, and logged.
func (c *lagCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ends, err := c.src.LogEndOffsets(ctx)
	if err != nil {
		c.logger.Error("promsink: lag collector log end offsets", "topic", c.topic, "error", err)
		return
	}

	for partition, logEnd := range ends {
		checkpoint, err := c.cp.Checkpoint(partition)
		if err != nil {
			c.logger.Error(
				"promsink: lag collector checkpoint",
				"topic",
				c.topic,
				"partition",
				partition,
				"error",
				err,
			)
			continue
		}

		lag := logEnd - checkpoint - 1
		if lag < 0 {
			lag = 0
		}

		ch <- prometheus.MustNewConstMetric(lagDesc, prometheus.GaugeValue, float64(lag),
			c.topic, strconv.Itoa(int(partition)))
	}
}
