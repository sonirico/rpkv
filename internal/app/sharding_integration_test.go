//go:build integration

package app_test

import (
	"context"
	"encoding/json"
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

// TestShardingPartitionAffine proves two apps configured with disjoint
// partition sets each index only their own partition and are blind to the
// other's, both in reads and in healthz.
func TestShardingPartitionAffine(t *testing.T) {
	adminClient, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
	require.NoError(t, err)
	t.Cleanup(adminClient.Close)

	admin := kadm.NewClient(adminClient)
	topic := newTestTopicName(t, "app-sharding-")

	createCtx, createCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer createCancel()
	createResp, err := admin.CreateTopic(createCtx, 2, 1, nil, topic)
	require.NoError(t, err)
	require.NoError(t, createResp.Err)

	producer, err := kgo.NewClient(
		kgo.SeedBrokers(rptest.Brokers()),
		kgo.RecordPartitioner(kgo.ManualPartitioner()),
	)
	require.NoError(t, err)
	t.Cleanup(producer.Close)

	produceRecords(t, producer,
		&kgo.Record{Topic: topic, Partition: 0, Key: []byte("k0"), Value: []byte("v0")},
		&kgo.Record{Topic: topic, Partition: 1, Key: []byte("k1"), Value: []byte("v1")},
	)

	cfg0 := config.Config{
		Brokers:         []string{rptest.Brokers()},
		Topics:          []string{topic},
		DataDir:         t.TempDir(),
		Listen:          "127.0.0.1:0",
		MetadataRefresh: time.Second,
		Partitions:      []int32{0},
	}
	cfg1 := config.Config{
		Brokers:         []string{rptest.Brokers()},
		Topics:          []string{topic},
		DataDir:         t.TempDir(),
		Listen:          "127.0.0.1:0",
		MetadataRefresh: time.Second,
		Partitions:      []int32{1},
	}

	baseURL0, stop0 := startTestApp(t, cfg0)
	t.Cleanup(stop0)
	baseURL1, stop1 := startTestApp(t, cfg1)
	t.Cleanup(stop1)

	client := &http.Client{Timeout: 5 * time.Second}

	require.Eventually(t, func() bool {
		resp, err := client.Get(baseURL0 + "/v1/kv/" + topic + "/k0")
		if err != nil {
			return false
		}
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode == http.StatusOK
	}, testEventually, testEventuallyTick, "key %q was never indexed by its owning app", "k0")

	require.Eventually(t, func() bool {
		resp, err := client.Get(baseURL1 + "/v1/kv/" + topic + "/k1")
		if err != nil {
			return false
		}
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode == http.StatusOK
	}, testEventually, testEventuallyTick, "key %q was never indexed by its owning app", "k1")

	t.Run("own key returns exact value, foreign key is 404", func(t *testing.T) {
		resp, err := client.Get(baseURL0 + "/v1/kv/" + topic + "/k0")
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "v0", string(body))

		resp, err = client.Get(baseURL0 + "/v1/kv/" + topic + "/k1")
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusNotFound, resp.StatusCode)

		resp, err = client.Get(baseURL1 + "/v1/kv/" + topic + "/k1")
		require.NoError(t, err)
		body, err = io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "v1", string(body))

		resp, err = client.Get(baseURL1 + "/v1/kv/" + topic + "/k0")
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("healthz reports only the owned partition", func(t *testing.T) {
		assertHealthzOwnsExactly(t, client, baseURL0, topic, 0)
		assertHealthzOwnsExactly(t, client, baseURL1, topic, 1)
	})
}

// assertHealthzOwnsExactly asserts baseURL's /healthz reports topic's
// partitions as exactly []int32{wantPartition} and a partition_count of 2.
func assertHealthzOwnsExactly(
	t *testing.T,
	client *http.Client,
	baseURL, topic string,
	wantPartition int32,
) {
	t.Helper()

	require.Eventually(t, func() bool {
		resp, err := client.Get(baseURL + "/healthz")
		if err != nil {
			return false
		}
		defer func() { require.NoError(t, resp.Body.Close()) }()
		if resp.StatusCode != http.StatusOK {
			return false
		}

		var health struct {
			Topics map[string]struct {
				Partitions []struct {
					Partition int32 `json:"partition"`
				} `json:"partitions"`
				PartitionCount int32 `json:"partition_count"`
			} `json:"topics"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
			return false
		}

		topicHealth, ok := health.Topics[topic]
		return ok && topicHealth.PartitionCount == 2 && len(topicHealth.Partitions) == 1 &&
			topicHealth.Partitions[0].Partition == wantPartition
	}, testEventually, testEventuallyTick, "healthz for topic %q never reported partition %d of 2", topic, wantPartition)
}
