package index_test

import (
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/vfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonirico/rpkv/index"
)

type testIndexFixture struct {
	Index *index.Index
	DB    *pebble.DB
}

func newTestIndex(tb testing.TB) testIndexFixture {
	tb.Helper()

	db, err := pebble.Open("", &pebble.Options{FS: vfs.NewMem()})
	require.NoError(tb, err)

	ix := index.NewIndex(db)
	tb.Cleanup(func() {
		assert.NoError(tb, ix.Close())
	})

	return testIndexFixture{Index: ix, DB: db}
}

func TestIndexCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	fx := newTestIndex(t)

	firstErr := fx.Index.Close()
	secondErr := fx.Index.Close()

	require.NoError(t, firstErr)
	assert.NoError(
		t,
		secondErr,
		"a second Close must be swallowed by sync.Once, not surface Pebble's already-closed error",
	)
}

func TestIndexApplyAndGet(t *testing.T) {
	type testCase struct {
		name      string
		batches   [][]index.Entry
		key       string
		wantFound bool
		wantValue index.Pointer
	}

	tests := []testCase{
		{
			name:      "absent key is not found",
			batches:   nil,
			key:       "missing",
			wantFound: false,
		},
		{
			name: "set then get returns the pointer",
			batches: [][]index.Entry{
				{{Key: []byte("k1"), Pointer: index.Pointer{Partition: 1, Offset: 10}}},
			},
			key:       "k1",
			wantFound: true,
			wantValue: index.Pointer{Partition: 1, Offset: 10},
		},
		{
			name: "later apply overwrites the pointer",
			batches: [][]index.Entry{
				{{Key: []byte("k1"), Pointer: index.Pointer{Partition: 1, Offset: 10}}},
				{{Key: []byte("k1"), Pointer: index.Pointer{Partition: 2, Offset: 99}}},
			},
			key:       "k1",
			wantFound: true,
			wantValue: index.Pointer{Partition: 2, Offset: 99},
		},
		{
			name: "later entry in the same apply wins",
			batches: [][]index.Entry{
				{
					{Key: []byte("k1"), Pointer: index.Pointer{Partition: 1, Offset: 10}},
					{Key: []byte("k1"), Pointer: index.Pointer{Partition: 2, Offset: 20}},
				},
			},
			key:       "k1",
			wantFound: true,
			wantValue: index.Pointer{Partition: 2, Offset: 20},
		},
		{
			name: "set then tombstone is not found",
			batches: [][]index.Entry{
				{{Key: []byte("k1"), Pointer: index.Pointer{Partition: 1, Offset: 10}}},
				{{Key: []byte("k1"), Tombstone: true}},
			},
			key:       "k1",
			wantFound: false,
		},
		{
			name: "tombstone for an absent key stays absent",
			batches: [][]index.Entry{
				{{Key: []byte("k1"), Tombstone: true}},
			},
			key:       "k1",
			wantFound: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fx := newTestIndex(t)
			for _, batch := range tc.batches {
				require.NoError(t, fx.Index.Apply(batch, nil))
			}

			got, err := fx.Index.Get([]byte(tc.key))

			require.NoError(t, err)
			assert.Equal(t, tc.wantFound, got.Found)
			if tc.wantFound {
				assert.Equal(t, tc.wantValue, got.Pointer)
			}
		})
	}
}

func TestIndexApplyEmptyEntriesWithCheckpointAdvance(t *testing.T) {
	t.Parallel()

	fx := newTestIndex(t)

	err := fx.Index.Apply(nil, map[int32]int64{2: 42})

	require.NoError(t, err)
	got, err := fx.Index.Checkpoint(2)
	require.NoError(t, err)
	assert.Equal(t, int64(42), got)
}

func TestIndexApplyIsIdempotent(t *testing.T) {
	t.Parallel()

	fx := newTestIndex(t)
	entries := []index.Entry{{Key: []byte("k1"), Pointer: index.Pointer{Partition: 1, Offset: 10}}}
	checkpoints := map[int32]int64{1: 10}

	require.NoError(t, fx.Index.Apply(entries, checkpoints))
	require.NoError(t, fx.Index.Apply(entries, checkpoints))

	gotLookup, err := fx.Index.Get([]byte("k1"))
	require.NoError(t, err)
	assert.Equal(
		t,
		index.Lookup{Pointer: index.Pointer{Partition: 1, Offset: 10}, Found: true},
		gotLookup,
	)

	gotCheckpoint, err := fx.Index.Checkpoint(1)
	require.NoError(t, err)
	assert.Equal(t, int64(10), gotCheckpoint)
}

func TestIndexCheckpoint(t *testing.T) {
	type testCase struct {
		name        string
		checkpoints []map[int32]int64
		partition   int32
		want        int64
	}

	tests := []testCase{
		{
			name:      "unknown partition returns -1",
			partition: 0,
			want:      -1,
		},
		{
			name:        "checkpoint written is returned",
			checkpoints: []map[int32]int64{{0: 5}},
			partition:   0,
			want:        5,
		},
		{
			name:        "later apply advances the checkpoint",
			checkpoints: []map[int32]int64{{0: 5}, {0: 9}},
			partition:   0,
			want:        9,
		},
		{
			name:        "other partitions do not interfere",
			checkpoints: []map[int32]int64{{1: 3}},
			partition:   0,
			want:        -1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fx := newTestIndex(t)
			for _, ck := range tc.checkpoints {
				require.NoError(t, fx.Index.Apply(nil, ck))
			}

			got, err := fx.Index.Checkpoint(tc.partition)

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

type testCrashedIndexFixture struct {
	Index     *index.Index
	Surviving index.Entry
	Lost      index.Entry
}

// newTestCrashedIndex applies one synced batch, then a second batch with
// syncs suppressed to simulate a crash before it reaches stable storage,
// closes and reopens on the same (crash-consistent) MemFS, and returns the
// reopened index. The second batch's entries and checkpoint must both be
// absent: SPEC requires index apply and checkpoint advance to commit in one
// batch, so a crash can never leave one without the other.
func newTestCrashedIndex(t *testing.T) testCrashedIndexFixture {
	t.Helper()

	fs := vfs.NewStrictMem()
	opts := &pebble.Options{FS: fs}

	db1, err := pebble.Open("", opts)
	require.NoError(t, err)
	ix1 := index.NewIndex(db1)

	surviving := index.Entry{Key: []byte("k1"), Pointer: index.Pointer{Partition: 0, Offset: 10}}
	require.NoError(t, ix1.Apply([]index.Entry{surviving}, map[int32]int64{0: 10}))

	lost := index.Entry{Key: []byte("k2"), Pointer: index.Pointer{Partition: 0, Offset: 20}}
	fs.SetIgnoreSyncs(true)
	require.NoError(t, ix1.Apply([]index.Entry{lost}, map[int32]int64{0: 20}))
	require.NoError(t, ix1.Close())
	fs.ResetToSyncedState()
	fs.SetIgnoreSyncs(false)

	db2, err := pebble.Open("", opts)
	require.NoError(t, err)
	ix2 := index.NewIndex(db2)
	t.Cleanup(func() {
		assert.NoError(t, ix2.Close())
	})

	return testCrashedIndexFixture{Index: ix2, Surviving: surviving, Lost: lost}
}

func TestIndexCrashRecovery(t *testing.T) {
	t.Run("unsynced batch is lost atomically", func(t *testing.T) {
		t.Parallel()

		fx := newTestCrashedIndex(t)

		gotCheckpoint, err := fx.Index.Checkpoint(0)
		require.NoError(t, err)
		assert.Equal(t, int64(10), gotCheckpoint)

		gotSurvivor, err := fx.Index.Get(fx.Surviving.Key)
		require.NoError(t, err)
		assert.Equal(t, index.Lookup{Pointer: fx.Surviving.Pointer, Found: true}, gotSurvivor)

		gotLost, err := fx.Index.Get(fx.Lost.Key)
		require.NoError(t, err)
		assert.False(t, gotLost.Found)
	})

	t.Run("reapplying the lost batch is idempotent", func(t *testing.T) {
		t.Parallel()

		fx := newTestCrashedIndex(t)

		require.NoError(t, fx.Index.Apply([]index.Entry{fx.Lost}, map[int32]int64{0: 20}))

		gotCheckpoint, err := fx.Index.Checkpoint(0)
		require.NoError(t, err)
		assert.Equal(t, int64(20), gotCheckpoint)

		gotLost, err := fx.Index.Get(fx.Lost.Key)
		require.NoError(t, err)
		assert.Equal(t, index.Lookup{Pointer: fx.Lost.Pointer, Found: true}, gotLost)
	})
}

func TestIndexLookupCorruptValue(t *testing.T) {
	type testCase struct {
		name     string
		rawKey   []byte
		badValue []byte
		act      func(fx testIndexFixture) error
	}

	tests := []testCase{
		{
			name:     "corrupt entry value surfaces from Get",
			rawKey:   append([]byte{0x01}, []byte("k1")...),
			badValue: []byte("short"),
			act: func(fx testIndexFixture) error {
				_, err := fx.Index.Get([]byte("k1"))
				return err
			},
		},
		{
			name:     "corrupt checkpoint value surfaces from Checkpoint",
			rawKey:   append([]byte{0x02}, 0, 0, 0, 3),
			badValue: []byte("short"),
			act: func(fx testIndexFixture) error {
				_, err := fx.Index.Checkpoint(3)
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fx := newTestIndex(t)
			require.NoError(t, fx.DB.Set(tc.rawKey, tc.badValue, pebble.Sync))

			err := tc.act(fx)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "corrupt value")
		})
	}
}
