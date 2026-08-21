package promsink

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAssignedPartitionsSource satisfies assignedPartitionsSource with a
// fixed count.
type fakeAssignedPartitionsSource struct {
	count int
}

func (f *fakeAssignedPartitionsSource) AssignedPartitions() int {
	return f.count
}

type testAssignedCollector struct {
	collector *assignedCollector
	src       *fakeAssignedPartitionsSource
}

func newTestAssignedCollector(topic string, count int) testAssignedCollector {
	src := &fakeAssignedPartitionsSource{count: count}
	return testAssignedCollector{
		collector: newAssignedCollector(topic, src),
		src:       src,
	}
}

func TestAssignedCollector(t *testing.T) {
	t.Parallel()

	t.Run("Describe sends the single assigned-partitions descriptor", func(t *testing.T) {
		t.Parallel()

		fx := newTestAssignedCollector("orders", 3)
		ch := make(chan *prometheus.Desc, 1)

		fx.collector.Describe(ch)
		close(ch)

		var descs []*prometheus.Desc
		for d := range ch {
			descs = append(descs, d)
		}

		require.Len(t, descs, 1)
		assert.Equal(t, assignedPartitionsDesc, descs[0])
	})

	t.Run("Collect emits the source's current partition count", func(t *testing.T) {
		t.Parallel()

		fx := newTestAssignedCollector("orders", 5)

		count := testutil.CollectAndCount(fx.collector, "rpkv_ingest_partitions_assigned")

		assert.Equal(t, 1, count)
	})

	t.Run("Collect reports the gathered metric value and name", func(t *testing.T) {
		t.Parallel()

		fx := newTestAssignedCollector("orders", 7)
		want := `
# HELP rpkv_ingest_partitions_assigned Partitions currently assigned for consumption, by topic.
# TYPE rpkv_ingest_partitions_assigned gauge
rpkv_ingest_partitions_assigned{topic="orders"} 7
`
		err := testutil.CollectAndCompare(
			fx.collector,
			strings.NewReader(want),
			"rpkv_ingest_partitions_assigned",
		)

		assert.NoError(t, err)
	})

	t.Run("RegisterAssignedPartitions succeeds for a new topic", func(t *testing.T) {
		t.Parallel()

		sinks, err := New(slog.Default())
		require.NoError(t, err)
		src := &fakeAssignedPartitionsSource{count: 2}

		err = sinks.RegisterAssignedPartitions("orders", src)

		assert.NoError(t, err)
	})

	t.Run(
		"RegisterAssignedPartitions on the same topic twice returns a wrapped error",
		func(t *testing.T) {
			t.Parallel()

			sinks, err := New(slog.Default())
			require.NoError(t, err)
			src := &fakeAssignedPartitionsSource{count: 2}
			require.NoError(t, sinks.RegisterAssignedPartitions("orders", src))

			err = sinks.RegisterAssignedPartitions("orders", src)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "promsink: register assigned partitions collector")
		},
	)
}
