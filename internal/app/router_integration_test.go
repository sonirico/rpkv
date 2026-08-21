//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sonirico/rpkv/internal/config"
	"github.com/sonirico/rpkv/internal/rptest"
)

// TestRouterAmbiguousKey proves the router fans a read out to every shard
// and, when more than one shard answers 200 for the same key, picks the
// highest-timestamp winner and flags the response ambiguous.
func TestRouterAmbiguousKey(t *testing.T) {
	adminClient, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
	require.NoError(t, err)
	t.Cleanup(adminClient.Close)

	admin := kadm.NewClient(adminClient)
	topic := newTestTopicName(t, "app-router-ambiguous-")

	createCtx, createCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer createCancel()
	createResp, err := admin.CreateTopic(createCtx, 1, 1, nil, topic)
	require.NoError(t, err)
	require.NoError(t, createResp.Err)

	producer, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
	require.NoError(t, err)
	t.Cleanup(producer.Close)

	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	produceRecords(t, producer,
		&kgo.Record{
			Topic:     topic,
			Key:       []byte("K"),
			Value:     []byte("v1"),
			Timestamp: baseTime,
		},
	)

	cfgA := config.Config{
		Brokers:         []string{rptest.Brokers()},
		Topics:          []string{topic},
		DataDir:         t.TempDir(),
		Listen:          "127.0.0.1:0",
		MetadataRefresh: time.Second,
		Partitions:      []int32{0},
	}
	baseURLA, stopA := startTestApp(t, cfgA)
	t.Cleanup(stopA)

	client := &http.Client{Timeout: 5 * time.Second}

	require.Eventually(t, func() bool {
		resp, err := client.Get(baseURLA + "/v1/kv/" + topic + "/K")
		if err != nil {
			return false
		}
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode == http.StatusOK
	}, testEventually, testEventuallyTick, "key %q was never indexed by shard A", "K")

	updateCtx, updateCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer updateCancel()
	updateResp, err := admin.UpdatePartitions(updateCtx, 2, topic)
	require.NoError(t, err)
	require.NoError(t, updateResp.Error())

	manualProducer, err := kgo.NewClient(
		kgo.SeedBrokers(rptest.Brokers()),
		kgo.RecordPartitioner(kgo.ManualPartitioner()),
	)
	require.NoError(t, err)
	t.Cleanup(manualProducer.Close)

	produceRecords(t, manualProducer,
		&kgo.Record{
			Topic:     topic,
			Partition: 1,
			Key:       []byte("K"),
			Value:     []byte("v2"),
			Timestamp: baseTime.Add(time.Second),
		},
	)

	cfgB := config.Config{
		Brokers:         []string{rptest.Brokers()},
		Topics:          []string{topic},
		DataDir:         t.TempDir(),
		Listen:          "127.0.0.1:0",
		MetadataRefresh: time.Second,
		Partitions:      []int32{1},
	}
	baseURLB, stopB := startTestApp(t, cfgB)
	t.Cleanup(stopB)

	require.Eventually(t, func() bool {
		resp, err := client.Get(baseURLB + "/v1/kv/" + topic + "/K")
		if err != nil {
			return false
		}
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode == http.StatusOK
	}, testEventually, testEventuallyTick, "key %q was never indexed by shard B", "K")

	addrA := strings.TrimPrefix(baseURLA, "http://")
	addrB := strings.TrimPrefix(baseURLB, "http://")

	routerCfg := config.Config{
		Mode:   config.ModeRouter,
		Shards: []string{addrA, addrB},
		Listen: "127.0.0.1:0",
	}
	baseURLRouter, stopRouter := startTestApp(t, routerCfg)
	t.Cleanup(stopRouter)

	t.Run("ambiguous key resolves to the highest-timestamp winner", func(t *testing.T) {
		resp, err := client.Get(baseURLRouter + "/v1/kv/" + topic + "/K")
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "v2", string(body))
		require.Equal(t, "true", resp.Header.Get("X-Rpkv-Ambiguous"))
	})

	t.Run("metrics reports one ambiguous key", func(t *testing.T) {
		resp, err := client.Get(baseURLRouter + "/metrics")
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Contains(t, string(body), "rpkv_router_ambiguous_keys_total 1")
	})

	t.Run("healthz reports router mode and both shards", func(t *testing.T) {
		resp, err := client.Get(baseURLRouter + "/healthz")
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var health struct {
			Mode   string   `json:"mode"`
			Shards []string `json:"shards"`
		}
		require.NoError(t, json.Unmarshal(body, &health))
		require.Equal(t, "router", health.Mode)
		require.ElementsMatch(t, []string{addrA, addrB}, health.Shards)
	})
}
