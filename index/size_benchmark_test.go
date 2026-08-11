package index_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonirico/rpkv/index"
)

const (
	sizeBenchKeys       = 1000000
	sizeBenchPartitions = 2
	sizeBenchBatch      = 1000
)

// sizeBenchResult is the JSON shape written to RPKV_BENCH_OUT: the on-disk
// footprint of the index after applying sizeBenchKeys entries.
type sizeBenchResult struct {
	Keys        int     `json:"keys"`
	Partitions  int     `json:"partitions"`
	BatchSize   int     `json:"batch_size"`
	BytesTotal  int64   `json:"bytes_total"`
	BytesPerKey float64 `json:"bytes_per_key"`
}

// TestIndexSizeBenchmark measures rpkv's own Pebble index footprint after
// applying sizeBenchKeys entries: no broker, no franz-go, just Index.Apply
// followed by summing every regular file under the Pebble directory.
func TestIndexSizeBenchmark(t *testing.T) {
	if os.Getenv("RPKV_BENCH") == "" {
		t.Skip("benchmark: set RPKV_BENCH=1 to run")
	}

	dir := t.TempDir()
	db, err := pebble.Open(dir, &pebble.Options{})
	require.NoError(t, err)

	ix := index.New(db)
	t.Cleanup(func() {
		assert.NoError(t, ix.Close())
	})

	for i := 0; i < sizeBenchKeys; i += sizeBenchBatch {
		end := i + sizeBenchBatch
		if end > sizeBenchKeys {
			end = sizeBenchKeys
		}

		batch := make([]index.Entry, 0, end-i)
		checkpoints := make(map[int32]int64)
		for j := i; j < end; j++ {
			partition := int32(j % sizeBenchPartitions)
			offset := int64(j / sizeBenchPartitions)
			batch = append(batch, index.Entry{
				Key:     []byte(fmt.Sprintf("k-%07d", j)),
				Pointer: index.Pointer{Partition: partition, Offset: offset},
			})
			if existing, ok := checkpoints[partition]; !ok || offset > existing {
				checkpoints[partition] = offset
			}
		}

		require.NoError(t, ix.Apply(batch, checkpoints))
	}

	require.NoError(t, ix.Close())

	var bytesTotal int64
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

	result := sizeBenchResult{
		Keys:        sizeBenchKeys,
		Partitions:  sizeBenchPartitions,
		BatchSize:   sizeBenchBatch,
		BytesTotal:  bytesTotal,
		BytesPerKey: float64(bytesTotal) / float64(sizeBenchKeys),
	}

	data, err := json.MarshalIndent(result, "", "  ")
	require.NoError(t, err)
	t.Logf("%s", data)

	if out := os.Getenv("RPKV_BENCH_OUT"); out != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(out), 0o755))
		require.NoError(t, os.WriteFile(out, append(data, '\n'), 0o644))
	}

	require.Positive(t, result.BytesTotal)
	require.Positive(t, result.BytesPerKey)
}
