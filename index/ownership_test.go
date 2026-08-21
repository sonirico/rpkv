package index_test

import (
	"errors"
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/vfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonirico/rpkv/index"
)

// newTestOwnershipIndex opens a fresh Index on its own in-memory Pebble
// directory and returns both the Index and the vfs.FS it lives on, so a
// test can close and reopen the same directory to exercise ownership
// persistence across restarts.
func newTestOwnershipIndex(tb testing.TB) (*index.Index, vfs.FS) {
	tb.Helper()

	fs := vfs.NewMem()
	db, err := pebble.Open("", &pebble.Options{FS: fs})
	require.NoError(tb, err)

	ix := index.New(db)
	tb.Cleanup(func() {
		assert.NoError(tb, ix.Close())
	})

	return ix, fs
}

// reopenTestOwnershipIndex closes ix and reopens the same directory on fs,
// returning the freshly opened Index.
func reopenTestOwnershipIndex(tb testing.TB, ix *index.Index, fs vfs.FS) *index.Index {
	tb.Helper()

	require.NoError(tb, ix.Close())

	db, err := pebble.Open("", &pebble.Options{FS: fs})
	require.NoError(tb, err)

	reopened := index.New(db)
	tb.Cleanup(func() {
		assert.NoError(tb, reopened.Close())
	})

	return reopened
}

// newTestLegacyOwnershipIndex opens a fresh Index, applies a checkpointed
// entry, then reopens it, returning an Index whose directory has
// checkpoints but no ownership record - the "legacy directory" shape that
// predates ownership tracking.
func newTestLegacyOwnershipIndex(tb testing.TB) (*index.Index, vfs.FS) {
	tb.Helper()

	ix, fs := newTestOwnershipIndex(tb)
	require.NoError(tb, ix.Apply(
		[]index.Entry{{Key: []byte("k1"), Pointer: index.Pointer{Partition: 0, Offset: 10}}},
		map[int32]int64{0: 10},
	))

	return reopenTestOwnershipIndex(tb, ix, fs), fs
}

func TestOwnership(t *testing.T) {
	t.Run("NewOwnership nil or empty means all", func(t *testing.T) {
		type testCase struct {
			name       string
			partitions []int32
		}

		tests := []testCase{
			{name: "nil", partitions: nil},
			{name: "empty", partitions: []int32{}},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				got := index.NewOwnership(tc.partitions)

				assert.True(t, got.All)
			})
		}
	})

	t.Run("NewOwnership sorts and dedups explicit partitions", func(t *testing.T) {
		t.Parallel()

		got := index.NewOwnership([]int32{3, 0, 3})

		assert.False(t, got.All)
		assert.Equal(t, []int32{0, 3}, got.Partitions)
	})

	t.Run("Owns", func(t *testing.T) {
		type testCase struct {
			name      string
			ownership index.Ownership
			partition int32
			want      bool
		}

		tests := []testCase{
			{
				name:      "all mode owns any partition",
				ownership: index.NewOwnership(nil),
				partition: 42,
				want:      true,
			},
			{
				name:      "explicit mode owns its own partition",
				ownership: index.NewOwnership([]int32{0, 3}),
				partition: 3,
				want:      true,
			},
			{
				name:      "explicit mode does not own a foreign partition",
				ownership: index.NewOwnership([]int32{0, 3}),
				partition: 7,
				want:      false,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				got := tc.ownership.Owns(tc.partition)

				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("EnsureOwnership adopts an empty directory and persists across reopen", func(t *testing.T) {
		t.Parallel()

		ix, fs := newTestOwnershipIndex(t)
		owned := index.NewOwnership([]int32{0})

		adoptErr := ix.EnsureOwnership(owned)
		require.NoError(t, adoptErr)

		reopened := reopenTestOwnershipIndex(t, ix, fs)
		confirmErr := reopened.EnsureOwnership(owned)

		assert.NoError(t, confirmErr)
	})

	t.Run("EnsureOwnership rejects a mismatching reopen", func(t *testing.T) {
		t.Parallel()

		ix, fs := newTestOwnershipIndex(t)
		require.NoError(t, ix.EnsureOwnership(index.NewOwnership([]int32{0})))

		reopened := reopenTestOwnershipIndex(t, ix, fs)
		mismatchErr := reopened.EnsureOwnership(index.NewOwnership([]int32{1}))

		require.Error(t, mismatchErr)
		assert.True(t, errors.Is(mismatchErr, index.ErrOwnershipMismatch))
	})

	t.Run("EnsureOwnership on a legacy directory with checkpoints but no record rejects a narrow claim", func(t *testing.T) {
		t.Parallel()

		legacy, _ := newTestLegacyOwnershipIndex(t)

		mismatchErr := legacy.EnsureOwnership(index.NewOwnership([]int32{0}))

		require.Error(t, mismatchErr)
		assert.True(t, errors.Is(mismatchErr, index.ErrOwnershipMismatch))
	})

	t.Run("EnsureOwnership on a legacy directory with checkpoints but no record accepts an all claim", func(t *testing.T) {
		t.Parallel()

		legacy, _ := newTestLegacyOwnershipIndex(t)

		allErr := legacy.EnsureOwnership(index.NewOwnership(nil))

		assert.NoError(t, allErr)
	})
}
