//go:build integration

package app_test

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sonirico/rpkv/clock"
	"github.com/sonirico/rpkv/internal/app"
	"github.com/sonirico/rpkv/internal/config"
	"github.com/sonirico/rpkv/internal/rptest"
)

const (
	compactionKeySpace           = 100
	compactionOverwritesPerRound = 1500
	compactionTombstonesPerRound = 15
	compactionFillerPerRound     = 400
	compactionFillerValueSize    = 512
	compactionFillerChunkSize    = 50
	compactionRounds             = 3
	compactionQuiesce            = 90 * time.Second
)

// newTestCompactionTopicName returns a unique topic name for one
// compaction test run, mirroring newTestAppTopicName.
func newTestCompactionTopicName(t *testing.T) string {
	t.Helper()

	suffix := make([]byte, 8)
	_, err := cryptorand.Read(suffix)
	require.NoError(t, err)

	return fmt.Sprintf("app-compact-%s", hex.EncodeToString(suffix))
}

// newTestCompactionTopic creates a two-partition, aggressively compacting
// topic, the recipe from fetch/fetcher_integration_test.go's "superseded
// after compaction" subtest, so the broker forces compaction repeatedly
// over the course of the test.
func newTestCompactionTopic(t *testing.T, admin *kadm.Client) string {
	t.Helper()

	topic := newTestCompactionTopicName(t)
	configs := map[string]*string{
		"cleanup.policy":        kadm.StringPtr("compact"),
		"max.compaction.lag.ms": kadm.StringPtr("100"),
		"segment.ms":            kadm.StringPtr("100"),
		"segment.bytes":         kadm.StringPtr("1024"),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	createResp, err := admin.CreateTopics(ctx, 2, 1, configs, topic)
	require.NoError(t, err)
	for _, r := range createResp {
		require.NoError(t, r.Err)
	}

	return topic
}

// startTestApp wires and runs an App against cfg, returning its base HTTP
// URL and a stop func that cancels the run and requires both a clean Run
// exit and a clean Close. Callers own calling stop exactly once per
// started app.
func startTestApp(t *testing.T, cfg config.Config) (string, func()) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := app.New(cfg, logger, clock.NewSystem())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() {
		runErr <- a.Run(ctx)
	}()

	addr, err := a.Addr(ctx)
	require.NoError(t, err)

	stop := func() {
		cancel()
		select {
		case err := <-runErr:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("app did not stop within 10s")
		}
		require.NoError(t, a.Close())
	}

	return "http://" + addr, stop
}

// waitForQuiescence polls /metrics until rpkv_ingest_lag reports zero for
// both of topic's partitions, meaning the ingester has fully caught up to
// the log end and the model comparison below is safe to run.
func waitForQuiescence(t *testing.T, client *http.Client, baseURL, topic string) {
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

		for _, pd := range []string{"0", "1"} {
			prefix := fmt.Sprintf("rpkv_ingest_lag{partition=%q,topic=%q} ", pd, topic)
			value, ok := findMetricLine(bodyStr, prefix)
			if !ok {
				return false
			}
			lag, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil || lag != 0 {
				return false
			}
		}
		return true
	}, compactionQuiesce, 200*time.Millisecond, "topic %q never reached quiescence", topic)
}

// assertModel checks every live key resolves to its model value and every
// tombstoned key 404s. Called only after waitForQuiescence, so plain
// require loops are enough - no Eventually needed.
func assertModel(
	t *testing.T,
	client *http.Client,
	baseURL, topic string,
	live map[string][]byte,
	tombstoned map[string]struct{},
) {
	t.Helper()

	for key, value := range live {
		resp, err := client.Get(baseURL + "/v1/kv/" + topic + "/" + key)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		require.Equal(t, http.StatusOK, resp.StatusCode, "key %q", key)
		require.Equal(t, value, body, "key %q", key)
	}

	for key := range tombstoned {
		resp, err := client.Get(baseURL + "/v1/kv/" + topic + "/" + key)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		require.Equal(t, http.StatusNotFound, resp.StatusCode, "key %q", key)
	}
}

func TestCompactionContract(t *testing.T) {
	adminClient, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
	require.NoError(t, err)
	t.Cleanup(adminClient.Close)
	admin := kadm.NewClient(adminClient)

	producer, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
	require.NoError(t, err)
	t.Cleanup(producer.Close)

	topic := newTestCompactionTopic(t, admin)

	dataDir := t.TempDir()
	cfg := config.Config{
		Brokers: []string{rptest.Brokers()},
		Topics:  []string{topic},
		DataDir: dataDir,
		Listen:  "127.0.0.1:0",
	}

	client := &http.Client{Timeout: 10 * time.Second}

	live := make(map[string][]byte)
	tombstoned := make(map[string]struct{})
	rng := rand.New(rand.NewSource(7))

	var baseURL string
	var stop func()
	seq := 0

	for round := 0; round < compactionRounds; round++ {
		for start := 0; start < compactionOverwritesPerRound; start += 100 {
			end := start + 100
			if end > compactionOverwritesPerRound {
				end = compactionOverwritesPerRound
			}

			records := make([]*kgo.Record, 0, end-start)
			keys := make([]string, 0, end-start)
			values := make([][]byte, 0, end-start)
			for i := start; i < end; i++ {
				key := fmt.Sprintf("k-%04d", rng.Intn(compactionKeySpace))
				value := []byte(fmt.Sprintf("r%d-s%d", round, seq))
				seq++

				records = append(records, &kgo.Record{Topic: topic, Key: []byte(key), Value: value})
				keys = append(keys, key)
				values = append(values, value)
			}

			produceCtx, produceCancel := context.WithTimeout(context.Background(), 60*time.Second)
			results := producer.ProduceSync(produceCtx, records...)
			produceCancel()
			for _, r := range results {
				require.NoError(t, r.Err)
			}

			for i, key := range keys {
				live[key] = values[i]
				delete(tombstoned, key)
			}
		}

		tried := make(map[string]struct{})
		tombstonedThisRound := 0
		for tombstonedThisRound < compactionTombstonesPerRound {
			key := fmt.Sprintf("k-%04d", rng.Intn(compactionKeySpace))
			if _, present := live[key]; !present {
				tried[key] = struct{}{}
				if len(tried) >= compactionKeySpace {
					break
				}
				continue
			}

			tombstoneCtx, tombstoneCancel := context.WithTimeout(
				context.Background(),
				60*time.Second,
			)
			result := producer.ProduceSync(
				tombstoneCtx,
				&kgo.Record{Topic: topic, Key: []byte(key), Value: nil},
			)
			tombstoneCancel()
			require.NoError(t, result.FirstErr())

			delete(live, key)
			tombstoned[key] = struct{}{}
			tombstonedThisRound++
		}

		fillerPrefix := fmt.Sprintf("fill-r%d", round)
		for start := 0; start < compactionFillerPerRound; start += compactionFillerChunkSize {
			end := start + compactionFillerChunkSize
			if end > compactionFillerPerRound {
				end = compactionFillerPerRound
			}

			records := make([]*kgo.Record, 0, end-start)
			keys := make([]string, 0, end-start)
			values := make([][]byte, 0, end-start)
			for i := start; i < end; i++ {
				value := make([]byte, compactionFillerValueSize)
				_, err := cryptorand.Read(value)
				require.NoError(t, err)

				key := fmt.Sprintf("%s-%04d", fillerPrefix, i)
				records = append(records, &kgo.Record{Topic: topic, Key: []byte(key), Value: value})
				keys = append(keys, key)
				values = append(values, value)
			}

			fillerCtx, fillerCancel := context.WithTimeout(context.Background(), 60*time.Second)
			results := producer.ProduceSync(fillerCtx, records...)
			fillerCancel()
			for _, r := range results {
				require.NoError(t, r.Err)
			}

			for i, key := range keys {
				live[key] = values[i]
			}
		}

		if round == 0 {
			baseURL, stop = startTestApp(t, cfg)
			t.Cleanup(func() { stop() })
		} else {
			stop()
			baseURL, stop = startTestApp(t, cfg)
		}

		waitForQuiescence(t, client, baseURL, topic)
		assertModel(t, client, baseURL, topic, live, tombstoned)
	}

	finalPrefix := "fill-final"
	records := make([]*kgo.Record, 0, compactionFillerChunkSize)
	keys := make([]string, 0, compactionFillerChunkSize)
	values := make([][]byte, 0, compactionFillerChunkSize)
	for i := 0; i < compactionFillerChunkSize; i++ {
		value := make([]byte, compactionFillerValueSize)
		_, err := cryptorand.Read(value)
		require.NoError(t, err)

		key := fmt.Sprintf("%s-%04d", finalPrefix, i)
		records = append(records, &kgo.Record{Topic: topic, Key: []byte(key), Value: value})
		keys = append(keys, key)
		values = append(values, value)
	}

	finalCtx, finalCancel := context.WithTimeout(context.Background(), 60*time.Second)
	finalResults := producer.ProduceSync(finalCtx, records...)
	finalCancel()
	for _, r := range finalResults {
		require.NoError(t, r.Err)
	}
	for i, key := range keys {
		live[key] = values[i]
	}

	waitForQuiescence(t, client, baseURL, topic)
	assertModel(t, client, baseURL, topic, live, tombstoned)
}
