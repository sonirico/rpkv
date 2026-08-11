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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sonirico/rpkv/internal/config"
	"github.com/sonirico/rpkv/internal/rptest"
)

const (
	rebuildBenchKeys         = 1000000
	rebuildBenchValueSize    = 256
	rebuildBenchProduceChunk = 1000
	rebuildBenchSeed         = 11
)

// rebuildBenchResult is the JSON shape written to RPKV_BENCH_OUT: the
// workload size alongside the wall-clock time to reach quiescence and the
// derived throughput.
type rebuildBenchResult struct {
	Keys       int     `json:"keys"`
	ValueSize  int     `json:"value_size"`
	Partitions int     `json:"partitions"`
	ElapsedS   float64 `json:"elapsed_s"`
	KeysPerS   float64 `json:"keys_per_s"`
}

// TestRebuildRateBenchmark measures client-observed wall time for a fresh
// rpkv to ingest an existing log into an empty index: from process start
// until /metrics reports zero ingest lag on every partition.
func TestRebuildRateBenchmark(t *testing.T) {
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

	topic := newTestTopicName(t, "bench-rebuild-")

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

	rng := rand.New(rand.NewSource(rebuildBenchSeed))

	produceChunked(
		t, producer, topic, rebuildBenchKeys, rebuildBenchProduceChunk,
		func(i int) string { return fmt.Sprintf("k-%06d", i) },
		func(i int) []byte {
			value := make([]byte, rebuildBenchValueSize)
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

	start := time.Now()
	baseURL, stop := startTestApp(t, cfg)
	t.Cleanup(func() { stop() })

	client := &http.Client{Timeout: 10 * time.Second}
	waitForQuiescence(t, client, baseURL, topic)
	elapsed := time.Since(start)

	result := rebuildBenchResult{
		Keys:       rebuildBenchKeys,
		ValueSize:  rebuildBenchValueSize,
		Partitions: 2,
		ElapsedS:   elapsed.Seconds(),
		KeysPerS:   float64(rebuildBenchKeys) / elapsed.Seconds(),
	}

	data, err := json.MarshalIndent(result, "", "  ")
	require.NoError(t, err)
	t.Logf("%s", data)

	if out := os.Getenv("RPKV_BENCH_OUT"); out != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(out), 0o755))
		require.NoError(t, os.WriteFile(out, append(data, '\n'), 0o644))
	}

	require.Positive(t, result.ElapsedS)
	require.Positive(t, result.KeysPerS)
}
