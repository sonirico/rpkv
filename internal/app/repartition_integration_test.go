//go:build integration

package app_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sonirico/rpkv/internal/config"
	"github.com/sonirico/rpkv/internal/rptest"
)

// TestAppRepartition proves ingest picks up partitions added to a topic
// after the app started, without a restart.
func TestAppRepartition(t *testing.T) {
	adminClient, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
	require.NoError(t, err)
	t.Cleanup(adminClient.Close)

	admin := kadm.NewClient(adminClient)
	topic := newTestTopicName(t, "app-repartition-")

	createCtx, createCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer createCancel()
	createResp, err := admin.CreateTopic(createCtx, 1, 1, nil, topic)
	require.NoError(t, err)
	require.NoError(t, createResp.Err)

	producer, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
	require.NoError(t, err)
	t.Cleanup(producer.Close)

	produceRecords(t, producer,
		&kgo.Record{Topic: topic, Key: []byte("k1"), Value: []byte("v1")},
	)

	cfg := config.Config{
		Brokers:         []string{rptest.Brokers()},
		Topics:          []string{topic},
		DataDir:         t.TempDir(),
		Listen:          "127.0.0.1:0",
		MetadataRefresh: time.Second,
	}

	baseURL, stop := startTestApp(t, cfg)
	t.Cleanup(stop)

	client := &http.Client{Timeout: 5 * time.Second}

	require.Eventually(t, func() bool {
		resp, err := client.Get(baseURL + "/v1/kv/" + topic + "/k1")
		if err != nil {
			return false
		}
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode == http.StatusOK
	}, testEventually, testEventuallyTick, "key %q was never indexed", "k1")

	updateCtx, updateCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer updateCancel()
	updateResp, err := admin.UpdatePartitions(updateCtx, 4, topic)
	require.NoError(t, err)
	require.NoError(t, updateResp.Error())
	producer.ForceMetadataRefresh()

	var newKey, newValue string
	produceCtx, produceCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer produceCancel()
	for i := 0; newKey == ""; i++ {
		key := fmt.Sprintf("new-%d", i)
		value := fmt.Sprintf("new-value-%d", i)
		results := producer.ProduceSync(
			produceCtx,
			&kgo.Record{Topic: topic, Key: []byte(key), Value: []byte(value)},
		)
		require.Len(t, results, 1)
		require.NoError(t, results[0].Err)
		if results[0].Record.Partition >= 1 {
			newKey = key
			newValue = value
		}
		require.Less(t, i, 1000, "no produced key landed on a partition >= 1")
	}

	newKeyURL := baseURL + "/v1/kv/" + topic + "/" + newKey
	require.Eventually(t, func() bool {
		resp, err := client.Get(newKeyURL)
		if err != nil {
			return false
		}
		defer func() { require.NoError(t, resp.Body.Close()) }()
		t.Logf("GET %s: %d", newKeyURL, resp.StatusCode)
		return resp.StatusCode == http.StatusOK
	}, testEventually, testEventuallyTick, "key %q on a new partition was never indexed", newKey)

	resp, err := client.Get(newKeyURL)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, newValue, string(body))

	require.Eventually(t, func() bool {
		resp, err := client.Get(baseURL + "/metrics")
		if err != nil {
			return false
		}
		defer func() { require.NoError(t, resp.Body.Close()) }()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		line, ok := findMetricLine(
			string(body),
			fmt.Sprintf(`rpkv_ingest_partitions_assigned{topic="%s"} `, topic),
		)
		return ok && line == "4"
	}, testEventually, testEventuallyTick, "rpkv_ingest_partitions_assigned never reported 4 for topic %q", topic)
}
