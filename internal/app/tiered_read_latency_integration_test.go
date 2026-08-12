//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sonirico/rpkv/internal/config"
	"github.com/sonirico/rpkv/internal/rptest"
)

const (
	tieredKeys         = 50000
	tieredValueSize    = 256
	tieredPartitions   = 2
	tieredProduceChunk = 1000
	tieredSeed         = 7
	tieredColdReads    = 300

	tieredEvictionPoll     = 500 * time.Millisecond
	tieredEvictionDeadline = 180 * time.Second
)

// tieredLatencySummary is the percentile/min/max/mean shape shared by the
// cold and warm phases in tieredBenchResult.
type tieredLatencySummary struct {
	P50Ms  float64 `json:"p50_ms"`
	P90Ms  float64 `json:"p90_ms"`
	P99Ms  float64 `json:"p99_ms"`
	MinMs  float64 `json:"min_ms"`
	MaxMs  float64 `json:"max_ms"`
	MeanMs float64 `json:"mean_ms"`
}

// tieredBenchResult is the JSON shape written to RPKV_BENCH_OUT: the
// workload size alongside cold (cache-trimmed, first touch after local
// eviction) and warm (re-read, no trim) latency summaries.
type tieredBenchResult struct {
	Keys      int                  `json:"keys"`
	Reads     int                  `json:"reads"`
	ValueSize int                  `json:"value_size"`
	Cold      tieredLatencySummary `json:"cold"`
	Warm      tieredLatencySummary `json:"warm"`
}

// cloudStorageStatus is the subset of GET
// /v1/cloud_storage/status/{topic}/{partition} this test polls: once
// local_log_start_offset is past cloud_log_start_offset, local segments
// covering the log's start have been evicted and reads for early keys must
// come from tiered storage.
type cloudStorageStatus struct {
	LocalLogStartOffset int64 `json:"local_log_start_offset"`
	CloudLogStartOffset int64 `json:"cloud_log_start_offset"`
}

// summarize computes a tieredLatencySummary from a sorted samples slice via
// the package's shared percentile/toMillis helpers.
func summarize(sorted []time.Duration) tieredLatencySummary {
	var sum time.Duration
	for _, s := range sorted {
		sum += s
	}

	return tieredLatencySummary{
		P50Ms:  toMillis(percentile(sorted, 50)),
		P90Ms:  toMillis(percentile(sorted, 90)),
		P99Ms:  toMillis(percentile(sorted, 99)),
		MinMs:  toMillis(sorted[0]),
		MaxMs:  toMillis(sorted[len(sorted)-1]),
		MeanMs: toMillis(sum / time.Duration(len(sorted))),
	}
}

// trimCloudStorageCache empties the tiered-storage read cache so the next
// read for an evicted key must fetch from object storage rather than a
// cached copy of a prior fetch.
func trimCloudStorageCache(t *testing.T, client *http.Client) {
	t.Helper()

	url := fmt.Sprintf(
		"http://%s/v1/cloud_storage/cache/trim?objects=0&bytes=0",
		rptest.AdminAddr(),
	)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// waitForLocalEviction polls each of topic's partitions' cloud storage
// status until local_log_start_offset has moved past cloud_log_start_offset,
// meaning local segments covering the log's start have been evicted and
// reads for early keys must come from tiered storage.
func waitForLocalEviction(t *testing.T, client *http.Client, topic string, partitions int) {
	t.Helper()

	deadline := time.Now().Add(tieredEvictionDeadline)
	var lastBodies []string

	for {
		allEvicted := true
		lastBodies = lastBodies[:0]

		for p := 0; p < partitions; p++ {
			url := fmt.Sprintf(
				"http://%s/v1/cloud_storage/status/%s/%d",
				rptest.AdminAddr(), topic, p,
			)
			resp, err := client.Get(url)
			if err != nil {
				allEvicted = false
				lastBodies = append(
					lastBodies,
					fmt.Sprintf("partition %d: request error: %s", p, err),
				)
				continue
			}

			body, err := io.ReadAll(resp.Body)
			closeErr := resp.Body.Close()
			require.NoError(t, err)
			require.NoError(t, closeErr)

			if resp.StatusCode != http.StatusOK {
				allEvicted = false
				lastBodies = append(
					lastBodies,
					fmt.Sprintf("partition %d: status %d: %s", p, resp.StatusCode, body),
				)
				continue
			}

			var status cloudStorageStatus
			require.NoError(t, json.Unmarshal(body, &status), "partition %d body: %s", p, body)
			lastBodies = append(lastBodies, fmt.Sprintf("partition %d: %s", p, body))

			if status.LocalLogStartOffset <= status.CloudLogStartOffset {
				allEvicted = false
			}
		}

		if allEvicted {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf(
				"topic %q partitions never reached local eviction within %s, last status: %v",
				topic, tieredEvictionDeadline, lastBodies,
			)
		}

		time.Sleep(tieredEvictionPoll)
	}
}

// TestTieredReadLatencyBenchmark measures client-observed end-to-end
// GET /v1/kv/{topic}/{key} wall time for keys whose local segments have
// been evicted by retention and must be served from tiered storage: a cold
// phase with the cache trimmed before each read (first touch), and a warm
// phase re-reading the same keys with the cache left alone. Requires
// RPKV_TEST_TIERED=1 so internal/rptest provisions Redpanda against a
// self-provisioned MinIO.
func TestTieredReadLatencyBenchmark(t *testing.T) {
	if os.Getenv("RPKV_BENCH") == "" {
		t.Skip("benchmark: set RPKV_BENCH=1 to run")
	}
	if os.Getenv("RPKV_TEST_TIERED") == "" {
		t.Skip("benchmark: set RPKV_TEST_TIERED=1 to run against a tiered-storage broker")
	}

	adminClient, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
	require.NoError(t, err)
	t.Cleanup(adminClient.Close)
	admin := kadm.NewClient(adminClient)

	producer, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
	require.NoError(t, err)
	t.Cleanup(producer.Close)

	topic := newTestTopicName(t, "bench-tiered-")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	topicConfigs := map[string]*string{
		"cleanup.policy":        kadm.StringPtr("delete"),
		"segment.bytes":         kadm.StringPtr("1048576"),
		"redpanda.remote.write": kadm.StringPtr("true"),
		"redpanda.remote.read":  kadm.StringPtr("true"),
	}
	createResp, err := admin.CreateTopics(ctx, tieredPartitions, 1, topicConfigs, topic)
	require.NoError(t, err)
	for _, r := range createResp {
		require.NoError(t, r.Err)
	}
	t.Cleanup(func() {
		deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer deleteCancel()

		deleteResp, err := admin.DeleteTopics(deleteCtx, topic)
		require.NoError(t, err)
		for _, r := range deleteResp {
			require.NoError(t, r.Err)
		}
	})

	rng := rand.New(rand.NewSource(tieredSeed))

	produceChunked(
		t, producer, topic, tieredKeys, tieredProduceChunk,
		func(i int) string { return fmt.Sprintf("k-%06d", i) },
		func(i int) []byte {
			value := make([]byte, tieredValueSize)
			_, err := rng.Read(value)
			require.NoError(t, err)
			return value
		},
	)

	cfg := config.Config{
		Brokers: []string{rptest.Brokers()},
		Topics:  []string{topic},
		DataDir: t.TempDir(),
		Listen:  "127.0.0.1:0",
	}

	baseURL, stop := startTestApp(t, cfg)
	t.Cleanup(func() { stop() })

	client := &http.Client{Timeout: 10 * time.Second}
	waitForQuiescence(t, client, baseURL, topic)

	alterCtx, alterCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer alterCancel()
	alterResp, err := admin.AlterTopicConfigs(
		alterCtx,
		[]kadm.AlterConfig{
			{
				Op:    kadm.SetConfig,
				Name:  "retention.local.target.bytes",
				Value: kadm.StringPtr("128"),
			},
		},
		topic,
	)
	require.NoError(t, err)
	for _, r := range alterResp {
		require.NoError(t, r.Err)
	}

	waitForLocalEviction(t, client, topic, tieredPartitions)

	coldKeys := make([]string, tieredColdReads)
	for i, idx := range rng.Perm(tieredKeys)[:tieredColdReads] {
		coldKeys[i] = fmt.Sprintf("k-%06d", idx)
	}

	coldSamples := make([]time.Duration, 0, tieredColdReads)
	for _, key := range coldKeys {
		trimCloudStorageCache(t, client)

		start := time.Now()
		resp, _ := getKV(t, client, baseURL, topic, key)
		elapsed := time.Since(start)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		coldSamples = append(coldSamples, elapsed)
	}

	warmSamples := make([]time.Duration, 0, tieredColdReads)
	for _, key := range coldKeys {
		start := time.Now()
		resp, _ := getKV(t, client, baseURL, topic, key)
		elapsed := time.Since(start)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		warmSamples = append(warmSamples, elapsed)
	}

	sort.Slice(coldSamples, func(i, j int) bool { return coldSamples[i] < coldSamples[j] })
	sort.Slice(warmSamples, func(i, j int) bool { return warmSamples[i] < warmSamples[j] })

	result := tieredBenchResult{
		Keys:      tieredKeys,
		Reads:     tieredColdReads,
		ValueSize: tieredValueSize,
		Cold:      summarize(coldSamples),
		Warm:      summarize(warmSamples),
	}

	data, err := json.MarshalIndent(result, "", "  ")
	require.NoError(t, err)
	t.Logf("%s", data)
	t.Logf("cold/warm p50 ratio: %.2f", result.Cold.P50Ms/result.Warm.P50Ms)

	if out := os.Getenv("RPKV_BENCH_OUT"); out != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(out), 0o755))
		require.NoError(t, os.WriteFile(out, append(data, '\n'), 0o644))
	}

	require.Positive(t, result.Cold.P50Ms)
	require.Positive(t, result.Cold.P99Ms)
	require.Positive(t, result.Cold.MeanMs)
	require.GreaterOrEqual(t, result.Cold.P99Ms, result.Cold.P50Ms)

	require.Positive(t, result.Warm.P50Ms)
	require.Positive(t, result.Warm.P99Ms)
	require.Positive(t, result.Warm.MeanMs)
	require.GreaterOrEqual(t, result.Warm.P99Ms, result.Warm.P50Ms)
}
