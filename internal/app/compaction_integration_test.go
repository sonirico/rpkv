//go:build integration

package app_test

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
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
	compactionKeySpace           = 100
	compactionOverwritesPerRound = 1500
	compactionOverwriteChunkSize = 100
	compactionTombstonesPerRound = 15
	compactionFillerPerRound     = 400
	compactionFillerValueSize    = 512
	compactionFillerChunkSize    = 50
	compactionRounds             = 3
	compactionQuiesce            = 90 * time.Second
)

// newTestCompactionTopic creates a two-partition, aggressively compacting
// topic, the recipe from fetch/fetcher_integration_test.go's "superseded
// after compaction" subtest, so the broker forces compaction repeatedly
// over the course of the test.
func newTestCompactionTopic(t *testing.T, admin *kadm.Client) string {
	t.Helper()

	topic := newTestTopicName(t, "app-compact-")
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

// produceChunked builds n records for topic in chunks of chunkSize, keying
// and valuing each record via keyFn/valueFn, and produces each chunk via
// produceRecords. It returns the keys and values in production order.
func produceChunked(
	t *testing.T,
	producer *kgo.Client,
	topic string,
	n, chunkSize int,
	keyFn func(i int) string,
	valueFn func(i int) []byte,
) (keys []string, values [][]byte) {
	t.Helper()

	keys = make([]string, 0, n)
	values = make([][]byte, 0, n)

	for start := 0; start < n; start += chunkSize {
		end := start + chunkSize
		if end > n {
			end = n
		}

		records := make([]*kgo.Record, 0, end-start)
		for i := start; i < end; i++ {
			key := keyFn(i)
			value := valueFn(i)

			records = append(records, &kgo.Record{Topic: topic, Key: []byte(key), Value: value})
			keys = append(keys, key)
			values = append(values, value)
		}

		produceRecords(t, producer, records...)
	}

	return keys, values
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

// getKV issues a GET for key against topic, reads and closes the response
// body, and hands back both the response and the body bytes so callers can
// inspect whatever subset of status, headers, and body they need without
// each re-deriving the same GET+read+close sequence.
func getKV(
	t *testing.T,
	client *http.Client,
	baseURL, topic, key string,
) (*http.Response, []byte) {
	t.Helper()

	resp, err := client.Get(baseURL + "/v1/kv/" + topic + "/" + key)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	return resp, body
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
		resp, body := getKV(t, client, baseURL, topic, key)

		require.Equal(t, http.StatusOK, resp.StatusCode, "key %q", key)
		require.Equal(t, value, body, "key %q", key)
	}

	for key := range tombstoned {
		resp, _ := getKV(t, client, baseURL, topic, key)

		require.Equal(t, http.StatusNotFound, resp.StatusCode, "key %q", key)
	}
}

// produceCompactionRound produces one round of overwrites, tombstones, and
// filler records against topic, mutating live/tombstoned/seq in place to
// track the model. Shared by TestCompactionContract and
// TestRebuildConvergence: both drive the identical overwrite/tombstone/
// filler shape and the same tried-set bail-out, differing only in their rng
// seed, overwrite value format, filler key prefix, and the tombstone
// failure message they want on the wire (a round index for the compaction
// test, none for the single-round rebuild test).
func produceCompactionRound(
	t *testing.T,
	producer *kgo.Client,
	topic string,
	rng *rand.Rand,
	live map[string][]byte,
	tombstoned map[string]struct{},
	seq *int,
	overwriteValueFn func(seq int) []byte,
	fillerKeyPrefix string,
	tombstoneFailMsgAndArgs ...interface{},
) {
	t.Helper()

	overwriteKeys, overwriteValues := produceChunked(
		t, producer, topic,
		compactionOverwritesPerRound, compactionOverwriteChunkSize,
		func(i int) string { return fmt.Sprintf("k-%04d", rng.Intn(compactionKeySpace)) },
		func(i int) []byte {
			value := overwriteValueFn(*seq)
			*seq++
			return value
		},
	)
	for i, key := range overwriteKeys {
		live[key] = overwriteValues[i]
		delete(tombstoned, key)
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

		produceRecords(t, producer, &kgo.Record{Topic: topic, Key: []byte(key), Value: nil})

		delete(live, key)
		tombstoned[key] = struct{}{}
		tombstonedThisRound++
	}
	require.Equal(
		t, compactionTombstonesPerRound, tombstonedThisRound,
		tombstoneFailMsgAndArgs...,
	)

	fillerKeys, fillerValues := produceChunked(
		t, producer, topic,
		compactionFillerPerRound, compactionFillerChunkSize,
		func(i int) string { return fmt.Sprintf("%s-%04d", fillerKeyPrefix, i) },
		func(i int) []byte {
			value := make([]byte, compactionFillerValueSize)
			_, err := cryptorand.Read(value)
			require.NoError(t, err)
			return value
		},
	)
	for i, key := range fillerKeys {
		live[key] = fillerValues[i]
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
		produceCompactionRound(
			t, producer, topic, rng, live, tombstoned, &seq,
			func(s int) []byte { return []byte(fmt.Sprintf("r%d-s%d", round, s)) },
			fmt.Sprintf("fill-r%d", round),
			"round %d: not enough live keys to tombstone", round,
		)

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

	t.Run("final flush without restart", func(t *testing.T) {
		finalPrefix := "fill-final"
		finalKeys, finalValues := produceChunked(
			t, producer, topic,
			compactionFillerChunkSize, compactionFillerChunkSize,
			func(i int) string { return fmt.Sprintf("%s-%04d", finalPrefix, i) },
			func(i int) []byte {
				value := make([]byte, compactionFillerValueSize)
				_, err := cryptorand.Read(value)
				require.NoError(t, err)
				return value
			},
		)
		for i, key := range finalKeys {
			live[key] = finalValues[i]
		}

		waitForQuiescence(t, client, baseURL, topic)
		assertModel(t, client, baseURL, topic, live, tombstoned)
	})
}
