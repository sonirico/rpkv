package index_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sonirico/rpkv/index"
)

const benchBatchSize = 1000

func newBenchEntries(n int) []index.Entry {
	entries := make([]index.Entry, n)
	for i := range entries {
		entries[i] = index.Entry{
			Key:     []byte(fmt.Sprintf("key-%d", i)),
			Pointer: index.Pointer{Partition: int32(i % 4), Offset: int64(i)},
		}
	}
	return entries
}

func BenchmarkApply(b *testing.B) {
	ix := newTestIndex(b).Index

	entries := newBenchEntries(benchBatchSize)
	checkpoints := map[int32]int64{0: 1, 1: 1, 2: 1, 3: 1}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		require.NoError(b, ix.Apply(entries, checkpoints))
	}
}

func BenchmarkGet(b *testing.B) {
	ix := newTestIndex(b).Index

	entries := newBenchEntries(benchBatchSize)
	require.NoError(b, ix.Apply(entries, nil))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := ix.Get(entries[i%len(entries)].Key)
		require.NoError(b, err)
	}
}
