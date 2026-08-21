package promsink

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonirico/rpkv/server"
)

// fakeShapeSource satisfies shapeSource with a fixed shape.
type fakeShapeSource struct {
	shape server.TopicShape
}

func (f *fakeShapeSource) Shape() server.TopicShape {
	return f.shape
}

type testShapeCollector struct {
	collector *shapeCollector
	src       *fakeShapeSource
}

func newTestShapeCollector(topic string, shape server.TopicShape) testShapeCollector {
	src := &fakeShapeSource{shape: shape}
	return testShapeCollector{
		collector: newShapeCollector(topic, src),
		src:       src,
	}
}

func TestShapeCollector(t *testing.T) {
	t.Parallel()

	t.Run("Describe sends both shape descriptors", func(t *testing.T) {
		t.Parallel()

		fx := newTestShapeCollector(
			"orders",
			server.TopicShape{PartitionCount: 3, CleanupPolicy: "compact"},
		)
		ch := make(chan *prometheus.Desc, 2)

		fx.collector.Describe(ch)
		close(ch)

		var descs []*prometheus.Desc
		for d := range ch {
			descs = append(descs, d)
		}

		require.Len(t, descs, 2)
		assert.Equal(t, topicPartitionsDesc, descs[0])
		assert.Equal(t, topicCompactedDesc, descs[1])
	})

	t.Run("Collect emits the source's current partition count", func(t *testing.T) {
		t.Parallel()

		fx := newTestShapeCollector(
			"orders",
			server.TopicShape{PartitionCount: 5, CleanupPolicy: "delete"},
		)

		count := testutil.CollectAndCount(fx.collector, "rpkv_topic_partitions")

		assert.Equal(t, 1, count)
	})

	t.Run(
		"Collect reports rpkv_topic_compacted as 1 when the policy contains compact",
		func(t *testing.T) {
			t.Parallel()

			fx := newTestShapeCollector(
				"orders",
				server.TopicShape{PartitionCount: 7, CleanupPolicy: "compact"},
			)
			want := `
# HELP rpkv_topic_compacted 1 if the topic's cleanup.policy contains compact, else 0.
# TYPE rpkv_topic_compacted gauge
rpkv_topic_compacted{topic="orders"} 1
`
			err := testutil.CollectAndCompare(
				fx.collector,
				strings.NewReader(want),
				"rpkv_topic_compacted",
			)

			assert.NoError(t, err)
		},
	)

	t.Run(
		"Collect reports rpkv_topic_compacted as 0 when the policy does not contain compact",
		func(t *testing.T) {
			t.Parallel()

			fx := newTestShapeCollector(
				"orders",
				server.TopicShape{PartitionCount: 7, CleanupPolicy: "delete"},
			)
			want := `
# HELP rpkv_topic_compacted 1 if the topic's cleanup.policy contains compact, else 0.
# TYPE rpkv_topic_compacted gauge
rpkv_topic_compacted{topic="orders"} 0
`
			err := testutil.CollectAndCompare(
				fx.collector,
				strings.NewReader(want),
				"rpkv_topic_compacted",
			)

			assert.NoError(t, err)
		},
	)

	t.Run("RegisterShape succeeds for a new topic", func(t *testing.T) {
		t.Parallel()

		sinks, err := New(slog.Default())
		require.NoError(t, err)
		src := &fakeShapeSource{
			shape: server.TopicShape{PartitionCount: 2, CleanupPolicy: "delete"},
		}

		err = sinks.RegisterShape("orders", src)

		assert.NoError(t, err)
	})

	t.Run(
		"RegisterShape on the same topic twice returns a wrapped error",
		func(t *testing.T) {
			t.Parallel()

			sinks, err := New(slog.Default())
			require.NoError(t, err)
			src := &fakeShapeSource{
				shape: server.TopicShape{PartitionCount: 2, CleanupPolicy: "delete"},
			}
			require.NoError(t, sinks.RegisterShape("orders", src))

			err = sinks.RegisterShape("orders", src)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "promsink: register shape collector")
		},
	)
}
