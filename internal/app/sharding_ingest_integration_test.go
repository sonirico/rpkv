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
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sonirico/rpkv/internal/config"
	"github.com/sonirico/rpkv/internal/rptest"
)

const (
	shardIngestKeys       = 1000000
	shardIngestValueSize  = 256
	shardIngestChunk      = 1000
	shardIngestSeed       = 13
	shardIngestPartitions = 4
	shardIngestQuiesce    = 10 * time.Minute
)

// shardIngestSide is the JSON shape for one side (monolith or shard) of the
// ingest-rate comparison: the key count it observed alongside the wall-clock
// time to reach quiescence and the derived throughput.
type shardIngestSide struct {
	Keys     int64   `json:"keys"`
	ElapsedS float64 `json:"elapsed_s"`
	KeysPerS float64 `json:"keys_per_s"`
}

// shardIngestResult is the JSON shape written to RPKV_BENCH_OUT: the
// workload shape alongside both sides of the comparison and their elapsed
// ratio.
type shardIngestResult struct {
	Keys                        int             `json:"keys"`
	ValueSize                   int             `json:"value_size"`
	Partitions                  int             `json:"partitions"`
	Monolith                    shardIngestSide `json:"monolith"`
	Shard                       shardIngestSide `json:"shard"`
	ShardToMonolithElapsedRatio float64         `json:"shard_to_monolith_elapsed_ratio"`
}

// waitForPartitionQuiescence polls /metrics until rpkv_ingest_lag reports
// zero for every partition in partitions, meaning the ingester has fully
// caught up to the log end on each. Generalizes waitForQuiescence (which is
// hardcoded to partitions 0 and 1) to an arbitrary partition set.
func waitForPartitionQuiescence(
	t *testing.T,
	client *http.Client,
	baseURL, topic string,
	partitions []int32,
) {
	t.Helper()

	require.Eventually(t, func() bool {
		resp, err := client.Get(baseURL + "/metrics")
		if err != nil {
			return false
		}
		body, err := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if err != nil || closeErr != nil {
			return false
		}
		bodyStr := string(body)

		for _, p := range partitions {
			prefix := fmt.Sprintf(
				"rpkv_ingest_lag{partition=%q,topic=%q} ",
				strconv.Itoa(int(p)), topic,
			)
			value, ok := findMetricLine(bodyStr, prefix)
			if !ok {
				return false
			}
			lag, err := strconv.ParseFloat(value, 64)
			if err != nil || lag != 0 {
				return false
			}
		}
		return true
	}, shardIngestQuiesce, 200*time.Millisecond, "topic %q never reached quiescence", topic)
}

// TestShardIngestRateBenchmark measures client-observed wall time to reach
// quiescence for the same topic under two configurations: a monolith rpkv
// owning every partition, and a single-shard rpkv owning only partition 0.
func TestShardIngestRateBenchmark(t *testing.T) {
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

	topic := newTestTopicName(t, "bench-shard-ingest-")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	createResp, err := admin.CreateTopics(ctx, shardIngestPartitions, 1, nil, topic)
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

	rng := rand.New(rand.NewSource(shardIngestSeed))

	produceChunked(
		t, producer, topic, shardIngestKeys, shardIngestChunk,
		func(i int) string { return fmt.Sprintf("k-%06d", i) },
		func(i int) []byte {
			value := make([]byte, shardIngestValueSize)
			_, err := rng.Read(value)
			require.NoError(t, err)
			return value
		},
	)

	client := &http.Client{Timeout: 10 * time.Second}

	monolithCfg := config.Config{
		Brokers: []string{rptest.Brokers()},
		Topics:  []string{topic},
		DataDir: t.TempDir(),
		Listen:  "127.0.0.1:0",
	}

	monolithStart := time.Now()
	monolithBaseURL, monolithStop := startTestApp(t, monolithCfg)
	waitForPartitionQuiescence(t, client, monolithBaseURL, topic, []int32{0, 1, 2, 3})
	monolithElapsed := time.Since(monolithStart)
	monolithStop()

	shardCfg := config.Config{
		Brokers:    []string{rptest.Brokers()},
		Topics:     []string{topic},
		DataDir:    t.TempDir(),
		Listen:     "127.0.0.1:0",
		Partitions: []int32{0},
	}

	shardStart := time.Now()
	shardBaseURL, shardStop := startTestApp(t, shardCfg)
	waitForPartitionQuiescence(t, client, shardBaseURL, topic, []int32{0})
	shardElapsed := time.Since(shardStart)
	shardStop()

	ctx2, cancel2 := context.WithTimeout(context.Background(), shardIngestQuiesce)
	defer cancel2()

	endOffsets, err := admin.ListEndOffsets(ctx2, topic)
	require.NoError(t, err)
	shardOffset, ok := endOffsets.Lookup(topic, 0)
	require.True(t, ok)

	result := shardIngestResult{
		Keys:       shardIngestKeys,
		ValueSize:  shardIngestValueSize,
		Partitions: shardIngestPartitions,
		Monolith: shardIngestSide{
			Keys:     int64(shardIngestKeys),
			ElapsedS: monolithElapsed.Seconds(),
			KeysPerS: float64(shardIngestKeys) / monolithElapsed.Seconds(),
		},
		Shard: shardIngestSide{
			Keys:     shardOffset.Offset,
			ElapsedS: shardElapsed.Seconds(),
			KeysPerS: float64(shardOffset.Offset) / shardElapsed.Seconds(),
		},
	}
	result.ShardToMonolithElapsedRatio = result.Shard.ElapsedS / result.Monolith.ElapsedS

	data, err := json.MarshalIndent(result, "", "  ")
	require.NoError(t, err)
	t.Logf("%s", data)

	if out := os.Getenv("RPKV_BENCH_OUT"); out != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(out), 0o755))
		require.NoError(t, os.WriteFile(out, append(data, '\n'), 0o644))
	}

	require.Positive(t, result.Monolith.ElapsedS)
	require.Positive(t, result.Shard.ElapsedS)
	require.Positive(t, result.Monolith.KeysPerS)
	require.Positive(t, result.Shard.KeysPerS)
	require.Positive(t, result.Shard.Keys)
	require.Less(t, result.Shard.Keys, result.Monolith.Keys)
}
