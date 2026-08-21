package index_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"

	"github.com/sonirico/rpkv/index"
)

const (
	shardSizeBenchKeys       = 1000000
	shardSizeBenchPartitions = 4
	shardSizeBenchBatch      = 1000
)

// shardSizeBenchSide is the on-disk footprint of one side (monolith or
// shard) of the comparison.
type shardSizeBenchSide struct {
	Keys        int     `json:"keys"`
	BytesTotal  int64   `json:"bytes_total"`
	BytesPerKey float64 `json:"bytes_per_key"`
}

// shardSizeBenchResult is the JSON shape written to RPKV_BENCH_OUT: the
// on-disk footprint of a monolithic index versus a single-partition shard
// after applying shardSizeBenchKeys candidate entries.
type shardSizeBenchResult struct {
	Keys                 int                `json:"keys"`
	Partitions           int                `json:"partitions"`
	BatchSize            int                `json:"batch_size"`
	Monolith             shardSizeBenchSide `json:"monolith"`
	Shard                shardSizeBenchSide `json:"shard"`
	ShardToMonolithRatio float64            `json:"shard_to_monolith_ratio"`
}

// applyShardSizeBench opens a fresh Pebble index, restricts ownership to
// owned when non-nil, applies shardSizeBenchKeys candidate entries in
// batches of shardSizeBenchBatch, and returns the on-disk footprint plus
// the number of entries actually applied.
func applyShardSizeBench(t *testing.T, owned []int32) (bytesTotal int64, keys int) {
	dir := t.TempDir()
	db, err := pebble.Open(dir, &pebble.Options{})
	require.NoError(t, err)

	ix := index.New(db)
	require.NoError(t, ix.EnsureOwnership(index.NewOwnership(owned)))

	var ownedSet map[int32]struct{}
	if owned != nil {
		ownedSet = make(map[int32]struct{}, len(owned))
		for _, p := range owned {
			ownedSet[p] = struct{}{}
		}
	}

	for i := 0; i < shardSizeBenchKeys; i += shardSizeBenchBatch {
		end := i + shardSizeBenchBatch
		if end > shardSizeBenchKeys {
			end = shardSizeBenchKeys
		}

		batch := make([]index.Entry, 0, end-i)
		checkpoints := make(map[int32]int64)
		for j := i; j < end; j++ {
			partition := int32(j % shardSizeBenchPartitions)
			offset := int64(j / shardSizeBenchPartitions)

			if ownedSet != nil {
				if _, ok := ownedSet[partition]; !ok {
					continue
				}
			}

			batch = append(batch, index.Entry{
				Key:     []byte(fmt.Sprintf("k-%07d", j)),
				Pointer: index.Pointer{Partition: partition, Offset: offset},
			})
			if existing, ok := checkpoints[partition]; !ok || offset > existing {
				checkpoints[partition] = offset
			}
		}

		require.NoError(t, ix.Apply(batch, checkpoints))
		keys += len(batch)
	}

	require.NoError(t, ix.Close())

	require.NoError(t, filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			info, infoErr := d.Info()
			if infoErr != nil {
				return infoErr
			}
			bytesTotal += info.Size()
		}
		return nil
	}))

	return bytesTotal, keys
}

// TestShardingIndexSizeBenchmark compares the on-disk footprint of a
// monolithic index (all partitions owned) against a single-partition
// shard, both built from the same shardSizeBenchKeys candidate entries.
func TestShardingIndexSizeBenchmark(t *testing.T) {
	if os.Getenv("RPKV_BENCH") == "" {
		t.Skip("benchmark: set RPKV_BENCH=1 to run")
	}

	monolithBytes, monolithKeys := applyShardSizeBench(t, nil)
	shardBytes, shardKeys := applyShardSizeBench(t, []int32{0})

	result := shardSizeBenchResult{
		Keys:       shardSizeBenchKeys,
		Partitions: shardSizeBenchPartitions,
		BatchSize:  shardSizeBenchBatch,
		Monolith: shardSizeBenchSide{
			Keys:        monolithKeys,
			BytesTotal:  monolithBytes,
			BytesPerKey: float64(monolithBytes) / float64(monolithKeys),
		},
		Shard: shardSizeBenchSide{
			Keys:        shardKeys,
			BytesTotal:  shardBytes,
			BytesPerKey: float64(shardBytes) / float64(shardKeys),
		},
	}
	result.ShardToMonolithRatio = float64(
		result.Shard.BytesTotal,
	) / float64(
		result.Monolith.BytesTotal,
	)

	data, err := json.MarshalIndent(result, "", "  ")
	require.NoError(t, err)
	t.Logf("%s", data)

	if out := os.Getenv("RPKV_BENCH_OUT"); out != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(out), 0o755))
		require.NoError(t, os.WriteFile(out, append(data, '\n'), 0o644))
	}

	require.Positive(t, result.Monolith.BytesTotal)
	require.Positive(t, result.Shard.BytesTotal)
	require.Less(t, result.Shard.BytesTotal, result.Monolith.BytesTotal)
	require.Positive(t, result.ShardToMonolithRatio)
}
