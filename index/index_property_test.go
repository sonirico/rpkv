package index_test

import (
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/vfs"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/sonirico/rpkv/index"
)

// TestIndexApplyMatchesNaiveMaterialization defends SPEC's correctness
// invariant: for any record sequence, index state equals a naive
// map[key]value materialization at the same checkpoint. Keys are drawn from
// a small alphabet so the same key collides across batches, exercising
// overwrite and tombstone ordering the way a real partition would.
func TestIndexApplyMatchesNaiveMaterialization(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		db, err := pebble.Open("", &pebble.Options{FS: vfs.NewMem()})
		require.NoError(rt, err)
		rt.Cleanup(func() {
			require.NoError(rt, db.Close())
		})
		ix := index.NewIndex(db)

		keyGen := rapid.SampledFrom([]string{"a", "b", "c", "d", "e"})
		partitionGen := rapid.Int32Range(0, 3)
		offsetGen := rapid.Int64Range(0, 1_000_000)

		naive := make(map[string]index.Pointer)
		checkpoints := make(map[int32]int64)

		numBatches := rapid.IntRange(1, 15).Draw(rt, "numBatches")
		for i := 0; i < numBatches; i++ {
			numEntries := rapid.IntRange(0, 6).Draw(rt, "numEntries")
			entries := make([]index.Entry, 0, numEntries)
			for j := 0; j < numEntries; j++ {
				key := keyGen.Draw(rt, "key")
				pointer := index.Pointer{
					Partition: partitionGen.Draw(rt, "partition"),
					Offset:    offsetGen.Draw(rt, "offset"),
				}
				tombstone := rapid.Bool().Draw(rt, "tombstone")
				entries = append(
					entries,
					index.Entry{Key: []byte(key), Pointer: pointer, Tombstone: tombstone},
				)

				if tombstone {
					delete(naive, key)
				} else {
					naive[key] = pointer
				}
			}

			batchPartition := partitionGen.Draw(rt, "checkpointPartition")
			batchOffset := offsetGen.Draw(rt, "checkpointOffset")
			checkpoints[batchPartition] = batchOffset

			require.NoError(rt, ix.Apply(entries, map[int32]int64{batchPartition: batchOffset}))
		}

		for _, key := range []string{"a", "b", "c", "d", "e"} {
			got, err := ix.Get([]byte(key))
			require.NoError(rt, err)

			want, ok := naive[key]
			require.Equal(rt, ok, got.Found)
			if ok {
				require.Equal(rt, want, got.Pointer)
			}
		}

		for partition, want := range checkpoints {
			got, err := ix.Checkpoint(partition)
			require.NoError(rt, err)
			require.Equal(rt, want, got)
		}
	})
}
