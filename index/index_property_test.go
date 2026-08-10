package index_test

import (
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/vfs"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/sonirico/rpkv/index"
)

// propertyTestKeys is the small key alphabet shared by every index property
// test. Keeping it small makes the same key collide across batches, which
// exercises overwrite and tombstone ordering the way a real partition would.
var propertyTestKeys = []string{"a", "b", "c", "d", "e"}

// propertyBatch is one randomly drawn Apply call: the entries and the single
// checkpoint advance that go into the same batch.
type propertyBatch struct {
	Entries             []index.Entry
	CheckpointPartition int32
	CheckpointOffset    int64
}

// newPropertyBatch draws one random batch shared by every index property
// test, so the generators cannot drift between properties.
func newPropertyBatch(rt *rapid.T) propertyBatch {
	keyGen := rapid.SampledFrom(propertyTestKeys)
	partitionGen := rapid.Int32Range(0, 3)
	offsetGen := rapid.Int64Range(0, 1_000_000)

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
	}

	return propertyBatch{
		Entries:             entries,
		CheckpointPartition: partitionGen.Draw(rt, "checkpointPartition"),
		CheckpointOffset:    offsetGen.Draw(rt, "checkpointOffset"),
	}
}

// TestIndexApplyMatchesNaiveMaterialization defends SPEC's correctness
// invariant: for any record sequence, index entry state equals a naive
// map[key]value materialization at the same checkpoint.
func TestIndexApplyMatchesNaiveMaterialization(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		db, err := pebble.Open("", &pebble.Options{FS: vfs.NewMem()})
		require.NoError(rt, err)
		rt.Cleanup(func() {
			require.NoError(rt, db.Close())
		})
		ix := index.NewIndex(db)

		naive := make(map[string]index.Pointer)

		numBatches := rapid.IntRange(1, 15).Draw(rt, "numBatches")
		for i := 0; i < numBatches; i++ {
			batch := newPropertyBatch(rt)
			for _, e := range batch.Entries {
				if e.Tombstone {
					delete(naive, string(e.Key))
				} else {
					naive[string(e.Key)] = e.Pointer
				}
			}

			checkpoints := map[int32]int64{batch.CheckpointPartition: batch.CheckpointOffset}
			require.NoError(rt, ix.Apply(batch.Entries, checkpoints))
		}

		for _, key := range propertyTestKeys {
			got, err := ix.Get([]byte(key))
			require.NoError(rt, err)

			want, ok := naive[key]
			require.Equal(rt, ok, got.Found)
			if ok {
				require.Equal(rt, want, got.Pointer)
			}
		}
	})
}

// TestIndexCheckpointMatchesLastApplied defends SPEC's correctness
// invariant: for any record sequence, the per-partition checkpoint equals
// the last offset applied for that partition.
func TestIndexCheckpointMatchesLastApplied(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		db, err := pebble.Open("", &pebble.Options{FS: vfs.NewMem()})
		require.NoError(rt, err)
		rt.Cleanup(func() {
			require.NoError(rt, db.Close())
		})
		ix := index.NewIndex(db)

		checkpoints := make(map[int32]int64)

		numBatches := rapid.IntRange(1, 15).Draw(rt, "numBatches")
		for i := 0; i < numBatches; i++ {
			batch := newPropertyBatch(rt)
			checkpoints[batch.CheckpointPartition] = batch.CheckpointOffset

			batchCheckpoints := map[int32]int64{batch.CheckpointPartition: batch.CheckpointOffset}
			require.NoError(rt, ix.Apply(batch.Entries, batchCheckpoints))
		}

		for partition, want := range checkpoints {
			got, err := ix.Checkpoint(partition)
			require.NoError(rt, err)
			require.Equal(rt, want, got)
		}
	})
}
