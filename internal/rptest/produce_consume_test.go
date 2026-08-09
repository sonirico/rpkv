//go:build integration

package rptest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

const testTimeout = 30 * time.Second

// testFixture wires the franz-go client and the unique topic a test
// produces to and consumes from.
type testFixture struct {
	client *kgo.Client
	topic  string
}

func newTestFixture(t *testing.T) testFixture {
	t.Helper()

	client, err := kgo.NewClient(
		kgo.SeedBrokers(Brokers()),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	require.NoError(t, err)
	t.Cleanup(client.Close)

	admin := kadm.NewClient(client)
	topic := newTestTopicName(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	createResp, err := admin.CreateTopics(ctx, 1, 1, nil, topic)
	require.NoError(t, err)
	for _, r := range createResp {
		require.NoError(t, r.Err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		deleteResp, err := admin.DeleteTopics(ctx, topic)
		require.NoError(t, err)
		for _, r := range deleteResp {
			require.NoError(t, r.Err)
		}
	})

	return testFixture{client: client, topic: topic}
}

func newTestTopicName(t *testing.T) string {
	t.Helper()

	suffix := make([]byte, 8)
	_, err := rand.Read(suffix)
	require.NoError(t, err)

	return fmt.Sprintf("rpkv-rptest-%s", hex.EncodeToString(suffix))
}

func TestProduceConsume(t *testing.T) {
	fx := newTestFixture(t)

	wantKey := []byte("rptest-key")
	wantValue := []byte("rptest-value")

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	produceResult := fx.client.ProduceSync(
		ctx,
		&kgo.Record{Topic: fx.topic, Key: wantKey, Value: wantValue},
	)
	require.NoError(t, produceResult.FirstErr())

	fx.client.AddConsumeTopics(fx.topic)

	var got *kgo.Record
	// Bounded by ctx.Err() rather than a fixed iteration count: the
	// deadline is the hang detector, so a record that never arrives fails
	// the test instead of spinning forever.
	for got == nil {
		require.NoError(t, ctx.Err(), "timed out waiting to consume the produced record")

		fetches := fx.client.PollFetches(ctx)
		fetches.EachError(func(_ string, _ int32, err error) {
			require.NoError(t, err)
		})
		fetches.EachRecord(func(r *kgo.Record) {
			got = r
		})
	}

	assert.Equal(t, wantKey, got.Key)
	assert.Equal(t, wantValue, got.Value)
}
