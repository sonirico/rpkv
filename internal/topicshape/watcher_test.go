package topicshape

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"

	"github.com/sonirico/rpkv/clock/clocktest"
	"github.com/sonirico/rpkv/server"
)

const testTopic = "orders"

// fakeTopicLister satisfies topicLister with a canned response.
type fakeTopicLister struct {
	topics kadm.TopicDetails
}

func (f *fakeTopicLister) ListTopics(
	_ context.Context,
	_ ...string,
) (kadm.TopicDetails, error) {
	return f.topics, nil
}

// fakeConfigDescriber satisfies configDescriber with a canned response.
type fakeConfigDescriber struct {
	configs kadm.ResourceConfigs
}

func (f *fakeConfigDescriber) DescribeTopicConfigs(
	_ context.Context,
	_ ...string,
) (kadm.ResourceConfigs, error) {
	return f.configs, nil
}

// recordingHandler is a slog.Handler that captures every record it
// receives, for asserting on log output.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.records = append(h.records, r)
	return nil
}

func (h *recordingHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }

func (h *recordingHandler) WithGroup(_ string) slog.Handler { return h }

func (h *recordingHandler) Records() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()

	out := make([]slog.Record, len(h.records))
	copy(out, h.records)
	return out
}

// newTestTopicDetails builds a kadm.TopicDetails with a single topic
// entry holding partitionCount partitions.
func newTestTopicDetails(topic string, partitionCount int) kadm.TopicDetails {
	partitions := make(kadm.PartitionDetails, partitionCount)
	for p := 0; p < partitionCount; p++ {
		partitions[int32(p)] = kadm.PartitionDetail{Topic: topic, Partition: int32(p)}
	}
	return kadm.TopicDetails{
		topic: {Topic: topic, Partitions: partitions},
	}
}

// newTestResourceConfigs builds a kadm.ResourceConfigs with a single
// resource holding a cleanup.policy config value.
func newTestResourceConfigs(topic, cleanupPolicy string) kadm.ResourceConfigs {
	value := cleanupPolicy
	return kadm.ResourceConfigs{
		{
			Name: topic,
			Configs: []kadm.Config{
				{Key: "cleanup.policy", Value: &value},
			},
		},
	}
}

type testWatcher struct {
	watcher   *Watcher
	lister    *fakeTopicLister
	describer *fakeConfigDescriber
	handler   *recordingHandler
}

func newTestWatcher(partitionCount int, cleanupPolicy string) testWatcher {
	lister := &fakeTopicLister{topics: newTestTopicDetails(testTopic, partitionCount)}
	describer := &fakeConfigDescriber{
		configs: newTestResourceConfigs(testTopic, cleanupPolicy),
	}
	clk := clocktest.NewMock(time.Unix(0, 0))
	handler := &recordingHandler{}
	logger := slog.New(handler)

	watcher := NewWatcher(lister, describer, testTopic, clk, time.Second, logger)

	return testWatcher{
		watcher:   watcher,
		lister:    lister,
		describer: describer,
		handler:   handler,
	}
}

func TestWatcher(t *testing.T) {
	t.Run("Refresh stores the partition count and cleanup policy", func(t *testing.T) {
		t.Parallel()

		fx := newTestWatcher(3, "compact")

		err := fx.watcher.Refresh(context.Background())

		require.NoError(t, err)
		assert.Equal(
			t,
			server.TopicShape{PartitionCount: 3, CleanupPolicy: "compact"},
			fx.watcher.Shape(),
		)
	})

	t.Run("Shape returns the last refreshed value", func(t *testing.T) {
		t.Parallel()

		fx := newTestWatcher(2, "delete")

		assert.Equal(t, server.TopicShape{}, fx.watcher.Shape())

		require.NoError(t, fx.watcher.Refresh(context.Background()))

		assert.Equal(
			t,
			server.TopicShape{PartitionCount: 2, CleanupPolicy: "delete"},
			fx.watcher.Shape(),
		)
	})

	t.Run("a cleanup.policy transition is logged once", func(t *testing.T) {
		t.Parallel()

		fx := newTestWatcher(2, "delete")

		require.NoError(t, fx.watcher.Refresh(context.Background()))
		assert.Empty(t, fx.handler.Records())

		fx.describer.configs = newTestResourceConfigs(testTopic, "compact")
		require.NoError(t, fx.watcher.Refresh(context.Background()))

		records := fx.handler.Records()
		require.Len(t, records, 1)
		assert.Equal(t, slog.LevelWarn, records[0].Level)

		require.NoError(t, fx.watcher.Refresh(context.Background()))
		assert.Len(t, fx.handler.Records(), 1)
	})

	t.Run("RunLoop returns when the context is cancelled", func(t *testing.T) {
		t.Parallel()

		fx := newTestWatcher(1, "delete")
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})

		go func() {
			fx.watcher.RunLoop(ctx)
			close(done)
		}()

		cancel()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for RunLoop to return")
		}
	})
}
