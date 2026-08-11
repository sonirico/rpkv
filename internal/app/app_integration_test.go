//go:build integration

package app_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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
	testEventually     = 30 * time.Second
	testEventuallyTick = 100 * time.Millisecond
)

// newTestTopicName returns a unique topic name for one test run, prefixed by
// prefix, mirroring fetch and ingest's integration topic naming.
func newTestTopicName(t *testing.T, prefix string) string {
	t.Helper()

	suffix := make([]byte, 8)
	_, err := rand.Read(suffix)
	require.NoError(t, err)

	return prefix + hex.EncodeToString(suffix)
}

// produceRecords produces records synchronously with a 60s timeout and
// requires every result to be error-free. Shared by every produce call
// site in this package.
func produceRecords(t *testing.T, producer *kgo.Client, records ...*kgo.Record) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	results := producer.ProduceSync(ctx, records...)
	for _, r := range results {
		require.NoError(t, r.Err)
	}
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

// findMetricLine returns the line of a Prometheus exposition body starting
// with prefix, with prefix stripped, and whether one was found.
func findMetricLine(body, prefix string) (string, bool) {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix), true
		}
	}
	return "", false
}

func TestAppEndToEnd(t *testing.T) {
	adminClient, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
	require.NoError(t, err)
	t.Cleanup(adminClient.Close)

	admin := kadm.NewClient(adminClient)
	topic := newTestTopicName(t, "app-e2e-")

	createCtx, createCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer createCancel()
	createResp, err := admin.CreateTopic(createCtx, 2, 1, nil, topic)
	require.NoError(t, err)
	require.NoError(t, createResp.Err)

	producer, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
	require.NoError(t, err)
	t.Cleanup(producer.Close)

	produceRecords(t, producer,
		&kgo.Record{Topic: topic, Key: []byte("k1"), Value: []byte("v1")},
		&kgo.Record{Topic: topic, Key: []byte("k2"), Value: []byte("v2")},
		&kgo.Record{Topic: topic, Key: []byte("k2"), Value: nil},
	)

	cfg := config.Config{
		Brokers: []string{rptest.Brokers()},
		Topics:  []string{topic},
		DataDir: t.TempDir(),
		Listen:  "127.0.0.1:0",
	}

	baseURL, stop := startTestApp(t, cfg)
	t.Cleanup(stop)

	client := &http.Client{Timeout: 5 * time.Second}

	t.Run("live key returns value and headers", func(t *testing.T) {
		url := baseURL + "/v1/kv/" + topic + "/k1"

		require.Eventually(t, func() bool {
			resp, err := client.Get(url)
			if err != nil {
				return false
			}
			require.NoError(t, resp.Body.Close())
			return resp.StatusCode == http.StatusOK
		}, testEventually, testEventuallyTick, "key %q was never indexed", "k1")

		resp, err := client.Get(url)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "v1", string(body))

		partition, err := strconv.ParseInt(resp.Header.Get("X-Rpkv-Partition"), 10, 32)
		require.NoError(t, err)
		require.GreaterOrEqual(t, partition, int64(0))
		require.LessOrEqual(t, partition, int64(1))

		offset, err := strconv.ParseInt(resp.Header.Get("X-Rpkv-Offset"), 10, 64)
		require.NoError(t, err)
		require.GreaterOrEqual(t, offset, int64(0))

		checkpoint, err := strconv.ParseInt(resp.Header.Get("X-Rpkv-Checkpoint"), 10, 64)
		require.NoError(t, err)
		require.GreaterOrEqual(t, checkpoint, offset)
	})

	t.Run("metrics endpoint", func(t *testing.T) {
		resp, err := client.Get(baseURL + "/metrics")
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		require.Equal(t, http.StatusOK, resp.StatusCode)

		bodyStr := string(body)
		require.Contains(t, bodyStr, "rpkv_fetch_outcomes_total")
		require.Contains(t, bodyStr, "rpkv_http_request_duration_seconds")
		require.Contains(t, bodyStr, "rpkv_ingest_apply_batch_size")
		require.Contains(t, bodyStr, "rpkv_ingest_lag")

		hitLine := fmt.Sprintf(`rpkv_fetch_outcomes_total{outcome="hit",topic="%s"} `, topic)
		value, ok := findMetricLine(bodyStr, hitLine)
		require.True(t, ok, "missing metric line %q in:\n%s", hitLine, bodyStr)

		valueFloat, err := strconv.ParseFloat(value, 64)
		require.NoError(t, err)
		require.GreaterOrEqual(t, valueFloat, 1.0)
	})

	t.Run("tombstoned key is 404", func(t *testing.T) {
		url := baseURL + "/v1/kv/" + topic + "/k2"

		require.Eventually(t, func() bool {
			resp, err := client.Get(url)
			if err != nil {
				return false
			}
			require.NoError(t, resp.Body.Close())
			return resp.StatusCode == http.StatusNotFound
		}, testEventually, testEventuallyTick, "key %q was never observed as tombstoned", "k2")
	})

	t.Run("unknown key is 404", func(t *testing.T) {
		resp, err := client.Get(baseURL + "/v1/kv/" + topic + "/nope")
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("unindexed topic is 404 with body", func(t *testing.T) {
		resp, err := client.Get(baseURL + "/v1/kv/other-topic/k1")
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.Equal(t, "topic not indexed", string(body))
	})

	t.Run("healthz reports the topic", func(t *testing.T) {
		resp, err := client.Get(baseURL + "/healthz")
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var health struct {
			Topics map[string]struct {
				Partitions []struct {
					Partition int32 `json:"partition"`
				} `json:"partitions"`
			} `json:"topics"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&health))
		require.NoError(t, resp.Body.Close())

		topicHealth, ok := health.Topics[topic]
		require.True(t, ok, "healthz response missing topic %q", topic)
		require.NotEmpty(t, topicHealth.Partitions)
	})
}
