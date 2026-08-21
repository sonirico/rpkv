//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sonirico/rpkv/internal/config"
	"github.com/sonirico/rpkv/internal/rptest"
)

const (
	routerBenchKeys         = 100000
	routerBenchReads        = 5000
	routerBenchWarmupReads  = 100
	routerBenchValueSize    = 256
	routerBenchProduceChunk = 1000
	routerBenchSeed         = 7
	routerBenchShards       = 4
)

// routerBenchSide is the percentile/min/max/mean shape shared by the
// monolith and router sides in routerBenchResult.
type routerBenchSide struct {
	P50Ms  float64 `json:"p50_ms"`
	P90Ms  float64 `json:"p90_ms"`
	P99Ms  float64 `json:"p99_ms"`
	MinMs  float64 `json:"min_ms"`
	MaxMs  float64 `json:"max_ms"`
	MeanMs float64 `json:"mean_ms"`
}

// routerBenchResult is the JSON shape written to RPKV_BENCH_OUT: the
// workload shape alongside both sides of the comparison and the router's
// p50 overhead over the monolith.
type routerBenchResult struct {
	Keys                int             `json:"keys"`
	Reads               int             `json:"reads"`
	ValueSize           int             `json:"value_size"`
	Shards              int             `json:"shards"`
	Monolith            routerBenchSide `json:"monolith"`
	Router              routerBenchSide `json:"router"`
	RouterOverheadP50Ms float64         `json:"router_overhead_p50_ms"`
}

// routerBenchSummary computes a routerBenchSide from a sorted samples slice
// via the package's shared percentile/toMillis helpers.
func routerBenchSummary(sorted []time.Duration) routerBenchSide {
	var sum time.Duration
	for _, s := range sorted {
		sum += s
	}

	return routerBenchSide{
		P50Ms:  toMillis(percentile(sorted, 50)),
		P90Ms:  toMillis(percentile(sorted, 90)),
		P99Ms:  toMillis(percentile(sorted, 99)),
		MinMs:  toMillis(sorted[0]),
		MaxMs:  toMillis(sorted[len(sorted)-1]),
		MeanMs: toMillis(sum / time.Duration(len(sorted))),
	}
}

// measureReads issues the first routerBenchWarmupReads keys unmeasured and
// the remaining reads measured, returning the measured samples sorted
// ascending.
func measureReads(
	t *testing.T,
	client *http.Client,
	baseURL, topic string,
	readKeys []string,
) []time.Duration {
	t.Helper()

	for _, key := range readKeys[:routerBenchWarmupReads] {
		resp, _ := getKV(t, client, baseURL, topic, key)
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}

	samples := make([]time.Duration, 0, routerBenchReads)
	for _, key := range readKeys[routerBenchWarmupReads:] {
		start := time.Now()
		resp, _ := getKV(t, client, baseURL, topic, key)
		elapsed := time.Since(start)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		samples = append(samples, elapsed)
	}

	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })

	return samples
}

// TestRouterReadLatencyBenchmark measures client-observed end-to-end
// GET /v1/kv/{topic}/{key} wall time against a monolith rpkv owning every
// partition versus a router fanning the same reads out to routerBenchShards
// shard rpkv processes, quantifying the router's added latency.
func TestRouterReadLatencyBenchmark(t *testing.T) {
	if os.Getenv("RPKV_BENCH") == "" {
		t.Skip("benchmark: set RPKV_BENCH=1 to run")
	}

	adminClient, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
	require.NoError(t, err)
	t.Cleanup(adminClient.Close)
	admin := kadm.NewClient(adminClient)

	producer, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
	require.NoError(t, err)
	t.Cleanup(producer.Close)

	topic := newTestTopicName(t, "bench-router-read-")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	createResp, err := admin.CreateTopics(ctx, routerBenchShards, 1, nil, topic)
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

	rng := rand.New(rand.NewSource(routerBenchSeed))

	produceChunked(
		t, producer, topic, routerBenchKeys, routerBenchProduceChunk,
		func(i int) string { return fmt.Sprintf("k-%06d", i) },
		func(i int) []byte {
			value := make([]byte, routerBenchValueSize)
			_, err := rng.Read(value)
			require.NoError(t, err)
			return value
		},
	)

	readKeys := make([]string, 0, routerBenchWarmupReads+routerBenchReads)
	for i := 0; i < routerBenchWarmupReads+routerBenchReads; i++ {
		readKeys = append(readKeys, fmt.Sprintf("k-%06d", rng.Intn(routerBenchKeys)))
	}

	client := &http.Client{Timeout: 10 * time.Second}

	monolithCfg := config.Config{
		Brokers: []string{rptest.Brokers()},
		Topics:  []string{topic},
		DataDir: t.TempDir(),
		Listen:  "127.0.0.1:0",
	}

	monolithBaseURL, monolithStop := startTestApp(t, monolithCfg)
	waitForPartitionQuiescence(t, client, monolithBaseURL, topic, []int32{0, 1, 2, 3})
	monolithSamples := measureReads(t, client, monolithBaseURL, topic, readKeys)
	monolithStop()

	shards := make([]string, routerBenchShards)
	shardStops := make([]func(), routerBenchShards)
	for i := 0; i < routerBenchShards; i++ {
		shardCfg := config.Config{
			Brokers:    []string{rptest.Brokers()},
			Topics:     []string{topic},
			DataDir:    t.TempDir(),
			Listen:     "127.0.0.1:0",
			Partitions: []int32{int32(i)},
		}
		shardBaseURL, shardStop := startTestApp(t, shardCfg)
		waitForPartitionQuiescence(t, client, shardBaseURL, topic, []int32{int32(i)})
		shards[i] = strings.TrimPrefix(shardBaseURL, "http://")
		shardStops[i] = shardStop
	}

	routerCfg := config.Config{
		Mode:   config.ModeRouter,
		Shards: shards,
		Listen: "127.0.0.1:0",
	}
	routerBaseURL, routerStop := startTestApp(t, routerCfg)
	routerSamples := measureReads(t, client, routerBaseURL, topic, readKeys)
	routerStop()

	for _, shardStop := range shardStops {
		shardStop()
	}

	result := routerBenchResult{
		Keys:      routerBenchKeys,
		Reads:     routerBenchReads,
		ValueSize: routerBenchValueSize,
		Shards:    routerBenchShards,
		Monolith:  routerBenchSummary(monolithSamples),
		Router:    routerBenchSummary(routerSamples),
	}
	result.RouterOverheadP50Ms = result.Router.P50Ms - result.Monolith.P50Ms

	data, err := json.MarshalIndent(result, "", "  ")
	require.NoError(t, err)
	t.Logf("%s", data)

	if out := os.Getenv("RPKV_BENCH_OUT"); out != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(out), 0o755))
		require.NoError(t, os.WriteFile(out, append(data, '\n'), 0o644))
	}

	require.Positive(t, result.Monolith.P50Ms)
	require.Positive(t, result.Monolith.P99Ms)
	require.Positive(t, result.Monolith.MeanMs)
	require.GreaterOrEqual(t, result.Monolith.P99Ms, result.Monolith.P50Ms)

	require.Positive(t, result.Router.P50Ms)
	require.Positive(t, result.Router.P99Ms)
	require.Positive(t, result.Router.MeanMs)
	require.GreaterOrEqual(t, result.Router.P99Ms, result.Router.P50Ms)
}
