// Package promsink is the Prometheus implementation of the metrics facade
// defined in metrics/: it is the only place in rpkv that imports
// prometheus, wired in from cmd/rpkv's app per ADR-007. Public packages
// only ever see the facade's Counter/Histogram/Gauge interfaces.
package promsink

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/sonirico/rpkv/fetch"
	"github.com/sonirico/rpkv/ingest"
	"github.com/sonirico/rpkv/server"
)

// Sinks owns a private Prometheus registry - never the global default
// registry - and every metric rpkv reports, handing out the facade types
// (fetch.Metrics, ingest.Metrics, server.Metrics) public packages accept
// through their WithMetrics options.
type Sinks struct {
	registry *prometheus.Registry
	logger   *slog.Logger

	fetchOutcomes    *prometheus.CounterVec
	supersedeRetries prometheus.Counter
	requestDuration  prometheus.Histogram
	applyBatchSize   *prometheus.HistogramVec
	nullKeysSkipped  *prometheus.CounterVec
}

// New builds a private registry and registers every rpkv metric onto it.
func New(logger *slog.Logger) (*Sinks, error) {
	s := &Sinks{
		registry: prometheus.NewRegistry(),
		logger:   logger,
		fetchOutcomes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "rpkv_fetch_outcomes_total",
			Help: "Count of fetch outcomes, by topic and outcome.",
		}, []string{"topic", "outcome"}),
		supersedeRetries: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "rpkv_supersede_retries_total",
			Help: "Count of supersede-retry cycles the server performed.",
		}),
		requestDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "rpkv_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		}),
		applyBatchSize: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "rpkv_ingest_apply_batch_size",
			Help:    "Size of each ingest Apply batch, by topic.",
			Buckets: []float64{1, 10, 50, 100, 500, 1000, 5000},
		}, []string{"topic"}),
		nullKeysSkipped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "rpkv_ingest_null_keys_skipped_total",
			Help: "Count of null-key records skipped during ingest, by topic.",
		}, []string{"topic"}),
	}

	collectors := []prometheus.Collector{
		s.fetchOutcomes,
		s.supersedeRetries,
		s.requestDuration,
		s.applyBatchSize,
		s.nullKeysSkipped,
	}
	for _, c := range collectors {
		if err := s.registry.Register(c); err != nil {
			return nil, fmt.Errorf("promsink: register collector: %w", err)
		}
	}

	return s, nil
}

// FetchMetrics returns the fetch.Metrics bound to topic's label values.
func (s *Sinks) FetchMetrics(topic string) fetch.Metrics {
	return fetch.Metrics{
		Hits:       s.fetchOutcomes.WithLabelValues(topic, "hit"),
		Superseded: s.fetchOutcomes.WithLabelValues(topic, "superseded"),
		Evicted:    s.fetchOutcomes.WithLabelValues(topic, "evicted"),
		Errors:     s.fetchOutcomes.WithLabelValues(topic, "error"),
	}
}

// IngestMetrics returns the ingest.Metrics bound to topic's label values.
func (s *Sinks) IngestMetrics(topic string) ingest.Metrics {
	return ingest.Metrics{
		ApplyBatchSize:  s.applyBatchSize.WithLabelValues(topic),
		NullKeysSkipped: s.nullKeysSkipped.WithLabelValues(topic),
	}
}

// ServerMetrics returns the server.Metrics shared across every topic.
func (s *Sinks) ServerMetrics() server.Metrics {
	return server.Metrics{
		RequestDuration:  s.requestDuration,
		SupersedeRetries: s.supersedeRetries,
	}
}

// Handler serves the registry's metrics in the Prometheus exposition
// format.
func (s *Sinks) Handler() http.Handler {
	return promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{})
}

// RegisterLag registers one lag collector for topic's per-partition
// checkpoint-to-log-end gap onto the registry.
func (s *Sinks) RegisterLag(topic string, cp checkpointReader, src logEndSource) error {
	c := newLagCollector(topic, cp, src, s.logger)
	if err := s.registry.Register(c); err != nil {
		return fmt.Errorf("promsink: register lag collector %q: %w", topic, err)
	}
	return nil
}

// RegisterAssignedPartitions registers one assigned-partitions collector
// for topic onto the registry.
func (s *Sinks) RegisterAssignedPartitions(topic string, src assignedPartitionsSource) error {
	c := newAssignedCollector(topic, src)
	if err := s.registry.Register(c); err != nil {
		return fmt.Errorf("promsink: register assigned partitions collector %q: %w", topic, err)
	}
	return nil
}
