//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sonirico/rpkv/internal/config"
	"github.com/sonirico/rpkv/internal/rptest"
)

func TestAppTopicShape(t *testing.T) {
	adminClient, err := kgo.NewClient(kgo.SeedBrokers(rptest.Brokers()))
	require.NoError(t, err)
	t.Cleanup(adminClient.Close)

	admin := kadm.NewClient(adminClient)
	topic := newTestTopicName(t, "app-shape-")

	cleanupPolicy := "compact"
	createCtx, createCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer createCancel()
	createResp, err := admin.CreateTopic(
		createCtx,
		2,
		1,
		map[string]*string{"cleanup.policy": &cleanupPolicy},
		topic,
	)
	require.NoError(t, err)
	require.NoError(t, createResp.Err)

	cfg := config.Config{
		Brokers:         []string{rptest.Brokers()},
		Topics:          []string{topic},
		DataDir:         t.TempDir(),
		Listen:          "127.0.0.1:0",
		MetadataRefresh: 100 * time.Millisecond,
	}

	baseURL, stop := startTestApp(t, cfg)
	t.Cleanup(stop)

	client := &http.Client{Timeout: 5 * time.Second}

	t.Run("healthz reports partition count and cleanup policy", func(t *testing.T) {
		require.Eventually(t, func() bool {
			resp, err := client.Get(baseURL + "/healthz")
			if err != nil {
				return false
			}
			defer resp.Body.Close()

			var health struct {
				Topics map[string]struct {
					PartitionCount int32  `json:"partition_count"`
					CleanupPolicy  string `json:"cleanup_policy"`
				} `json:"topics"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
				return false
			}

			topicHealth, ok := health.Topics[topic]
			return ok && topicHealth.PartitionCount == 2 && topicHealth.CleanupPolicy == "compact"
		}, testEventually, testEventuallyTick, "healthz never reported the topic's shape")
	})

	t.Run("metrics endpoint reports partition count and compacted flag", func(t *testing.T) {
		partitionsLine := fmt.Sprintf(`rpkv_topic_partitions{topic="%s"} `, topic)
		compactedLine := fmt.Sprintf(`rpkv_topic_compacted{topic="%s"} `, topic)

		require.Eventually(t, func() bool {
			resp, err := client.Get(baseURL + "/metrics")
			if err != nil {
				return false
			}
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, resp.Body.Close())
			if err != nil {
				return false
			}
			bodyStr := string(body)

			partitions, ok := findMetricLine(bodyStr, partitionsLine)
			if !ok {
				return false
			}
			partitionsValue, err := strconv.ParseFloat(partitions, 64)
			if err != nil || partitionsValue != 2 {
				return false
			}

			compacted, ok := findMetricLine(bodyStr, compactedLine)
			if !ok {
				return false
			}
			compactedValue, err := strconv.ParseFloat(compacted, 64)
			return err == nil && compactedValue == 1
		}, testEventually, testEventuallyTick, "metrics never reported the topic's shape")
	})
}
