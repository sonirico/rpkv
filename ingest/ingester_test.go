package ingest

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sonirico/rpkv/index"
)

// newTestIngester wires an Ingester over a kgo.Client that does not dial
// until first use, so it is constructible and inspectable without a live
// broker.
func newTestIngester(t *testing.T) *Ingester {
	t.Helper()

	client, err := kgo.NewClient(kgo.SeedBrokers("127.0.0.1:9092"))
	require.NoError(t, err)
	t.Cleanup(client.Close)

	return New(client, nil, "orders", slog.Default())
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("wires the topic and starts with no assigned partitions", func(t *testing.T) {
		t.Parallel()

		in := newTestIngester(t)

		assert.Equal(t, "orders", in.topic)
		assert.Equal(t, 0, in.AssignedPartitions())
	})
}

func TestAssignedPartitions(t *testing.T) {
	t.Parallel()

	t.Run("reports zero before any partition sync", func(t *testing.T) {
		t.Parallel()

		in := newTestIngester(t)

		got := in.AssignedPartitions()

		assert.Equal(t, 0, got)
	})
}

func TestResumeOffset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		checkpoint int64
		want       kgo.Offset
	}{
		{
			name:       "no checkpoint resumes at log start",
			checkpoint: -1,
			want:       kgo.NewOffset().AtStart(),
		},
		{
			name:       "checkpoint zero resumes at offset one",
			checkpoint: 0,
			want:       kgo.NewOffset().At(1),
		},
		{
			name:       "checkpoint forty-one resumes at offset forty-two",
			checkpoint: 41,
			want:       kgo.NewOffset().At(42),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := resumeOffset(tc.checkpoint)

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestCollectEntries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		records     []*kgo.Record
		wantEntries []index.Entry
		wantSkipped int64
	}{
		{
			name: "value record maps to pointer entry",
			records: []*kgo.Record{
				{Key: []byte("k1"), Value: []byte("v1"), Partition: 0, Offset: 10},
			},
			wantEntries: []index.Entry{
				{
					Key:       []byte("k1"),
					Pointer:   index.Pointer{Partition: 0, Offset: 10},
					Tombstone: false,
				},
			},
			wantSkipped: 0,
		},
		{
			name: "nil value maps to tombstone",
			records: []*kgo.Record{
				{Key: []byte("k1"), Value: nil, Partition: 0, Offset: 11},
			},
			wantEntries: []index.Entry{
				{
					Key:       []byte("k1"),
					Pointer:   index.Pointer{Partition: 0, Offset: 11},
					Tombstone: true,
				},
			},
			wantSkipped: 0,
		},
		{
			name: "nil key is skipped and counted",
			records: []*kgo.Record{
				{Key: nil, Value: []byte("v1"), Partition: 0, Offset: 12},
			},
			wantEntries: []index.Entry{},
			wantSkipped: 1,
		},
		{
			name: "empty non-nil key is not skipped",
			records: []*kgo.Record{
				{Key: []byte{}, Value: []byte("v1"), Partition: 0, Offset: 13},
			},
			wantEntries: []index.Entry{
				{Key: []byte{}, Pointer: index.Pointer{Partition: 0, Offset: 13}, Tombstone: false},
			},
			wantSkipped: 0,
		},
		{
			name: "mixed batch preserves order",
			records: []*kgo.Record{
				{Key: []byte("k1"), Value: []byte("v1"), Partition: 0, Offset: 20},
				{Key: nil, Value: []byte("v2"), Partition: 0, Offset: 21},
				{Key: []byte("k2"), Value: nil, Partition: 0, Offset: 22},
			},
			wantEntries: []index.Entry{
				{
					Key:       []byte("k1"),
					Pointer:   index.Pointer{Partition: 0, Offset: 20},
					Tombstone: false,
				},
				{
					Key:       []byte("k2"),
					Pointer:   index.Pointer{Partition: 0, Offset: 22},
					Tombstone: true,
				},
			},
			wantSkipped: 1,
		},
		{
			name:        "empty input yields empty entries and zero skipped",
			records:     []*kgo.Record{},
			wantEntries: []index.Entry{},
			wantSkipped: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotEntries, gotSkipped := collectEntries(tc.records)

			assert.Equal(t, tc.wantEntries, gotEntries)
			assert.Equal(t, tc.wantSkipped, gotSkipped)
		})
	}
}
