package clock_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonirico/rpkv/clock"
)

func TestSystem(t *testing.T) {
	t.Run("Now is close to the wall clock and monotonic", func(t *testing.T) {
		t.Parallel()

		sut := clock.NewSystem()

		before := time.Now()
		got := sut.Now()
		after := time.Now()

		assert.False(t, got.Before(before))
		assert.False(t, got.After(after))
	})

	t.Run("After fires", func(t *testing.T) {
		t.Parallel()

		sut := clock.NewSystem()

		select {
		case <-sut.After(0):
		case <-time.After(2 * time.Second):
			require.Fail(t, "After(0) did not fire within the test timeout")
		}
	})
}
