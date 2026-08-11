//go:build integration

// TestReadLatencyBenchmark measures client-observed end-to-end
// GET /v1/kv/{topic}/{key} wall time on loopback against a self-provisioned
// broker with local segments: it includes the broker fetch round-trip, not
// just server-side handling. Timing is taken client-side rather than read
// from the server's request-duration histogram because that histogram uses
// DefBuckets, too coarse to resolve p50/p90/p99 at this scale.
package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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
	benchKeys         = 100000
	benchReads        = 5000
	benchWarmupReads  = 100
	benchValueSize    = 256
	benchProduceChunk = 1000
	benchSeed         = 7
)

// benchResult is the JSON shape written to RPKV_BENCH_OUT: the sample size
// alongside the latency percentiles computed from it.
type benchResult struct {
	Keys      int     `json:"keys"`
	Reads     int     `json:"reads"`
	ValueSize int     `json:"value_size"`
	P50Ms     float64 `json:"p50_ms"`
	P90Ms     float64 `json:"p90_ms"`
	P99Ms     float64 `json:"p99_ms"`
	MinMs     float64 `json:"min_ms"`
	MaxMs     float64 `json:"max_ms"`
	MeanMs    float64 `json:"mean_ms"`
}

// percentile returns the nearest-rank q-th percentile (0-100) of a sorted
// samples slice.
func percentile(sorted []time.Duration, q float64) time.Duration {
	idx := int(math.Ceil(q/100*float64(len(sorted)))) - 1
	return sorted[idx]
}

func toMillis(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

func TestReadLatencyBenchmark(t *testing.T) {
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

	topic := newTestTopicName(t, "bench-read-")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	createResp, err := admin.CreateTopics(ctx, 2, 1, nil, topic)
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

	rng := rand.New(rand.NewSource(benchSeed))

	produceChunked(
		t, producer, topic, benchKeys, benchProduceChunk,
		func(i int) string { return fmt.Sprintf("k-%06d", i) },
		func(i int) []byte {
			value := make([]byte, benchValueSize)
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

	for i := 0; i < benchWarmupReads; i++ {
		key := fmt.Sprintf("k-%06d", rng.Intn(benchKeys))
		resp, _ := getKV(t, client, baseURL, topic, key)
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}

	samples := make([]time.Duration, 0, benchReads)
	for i := 0; i < benchReads; i++ {
		key := fmt.Sprintf("k-%06d", rng.Intn(benchKeys))
		start := time.Now()
		resp, _ := getKV(t, client, baseURL, topic, key)
		elapsed := time.Since(start)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		samples = append(samples, elapsed)
	}

	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })

	var sum time.Duration
	for _, s := range samples {
		sum += s
	}

	result := benchResult{
		Keys:      benchKeys,
		Reads:     benchReads,
		ValueSize: benchValueSize,
		P50Ms:     toMillis(percentile(samples, 50)),
		P90Ms:     toMillis(percentile(samples, 90)),
		P99Ms:     toMillis(percentile(samples, 99)),
		MinMs:     toMillis(samples[0]),
		MaxMs:     toMillis(samples[len(samples)-1]),
		MeanMs:    toMillis(sum / time.Duration(len(samples))),
	}

	data, err := json.MarshalIndent(result, "", "  ")
	require.NoError(t, err)
	t.Logf("%s", data)

	if out := os.Getenv("RPKV_BENCH_OUT"); out != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(out), 0o755))
		require.NoError(t, os.WriteFile(out, append(data, '\n'), 0o644))
	}

	require.Positive(t, result.P50Ms)
	require.Positive(t, result.P99Ms)
	require.Positive(t, result.MeanMs)
	require.GreaterOrEqual(t, result.P99Ms, result.P50Ms)
}
