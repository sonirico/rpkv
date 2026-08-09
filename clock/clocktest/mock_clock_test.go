package clocktest_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonirico/rpkv/clock/clocktest"
)

var testStart = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newTestMockClock(t *testing.T) *clocktest.MockClock {
	t.Helper()

	return clocktest.NewMockClock(testStart)
}

func requireFired(t *testing.T, ch <-chan time.Time, want time.Time) {
	t.Helper()

	select {
	case got := <-ch:
		assert.Equal(t, want, got)
	default:
		require.Fail(t, "channel did not fire when expected")
	}
}

func requireNotFired(t *testing.T, ch <-chan time.Time) {
	t.Helper()

	select {
	case <-ch:
		require.Fail(t, "channel fired before it was expected to")
	default:
	}
}

func TestMockClock_Now(t *testing.T) {
	t.Run("returns the start time before any Advance", func(t *testing.T) {
		t.Parallel()

		sut := newTestMockClock(t)

		assert.Equal(t, testStart, sut.Now())
	})
}

func TestMockClock_Advance(t *testing.T) {
	type testCase struct {
		name  string
		steps []time.Duration
		want  time.Time
	}

	tests := []testCase{
		{
			name:  "single step moves now by exactly d",
			steps: []time.Duration{5 * time.Second},
			want:  testStart.Add(5 * time.Second),
		},
		{
			name:  "multiple steps accumulate",
			steps: []time.Duration{time.Second, 2 * time.Second, 3 * time.Second},
			want:  testStart.Add(6 * time.Second),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sut := newTestMockClock(t)

			for _, step := range tc.steps {
				sut.Advance(step)
			}

			assert.Equal(t, tc.want, sut.Now())
		})
	}
}

func TestMockClock_After(t *testing.T) {
	t.Run("does not fire before Advance reaches the deadline", func(t *testing.T) {
		t.Parallel()

		sut := newTestMockClock(t)
		ch := sut.After(10 * time.Second)

		sut.Advance(5 * time.Second)

		requireNotFired(t, ch)
	})

	t.Run("fires when Advance reaches the deadline exactly", func(t *testing.T) {
		t.Parallel()

		sut := newTestMockClock(t)
		deadline := testStart.Add(10 * time.Second)
		ch := sut.After(10 * time.Second)

		sut.Advance(10 * time.Second)

		requireFired(t, ch, deadline)
	})

	t.Run("fires when Advance passes the deadline", func(t *testing.T) {
		t.Parallel()

		sut := newTestMockClock(t)
		deadline := testStart.Add(10 * time.Second)
		ch := sut.After(10 * time.Second)

		sut.Advance(15 * time.Second)

		requireFired(t, ch, deadline)
	})

	t.Run(
		"fires immediately when the deadline has already passed at registration",
		func(t *testing.T) {
			t.Parallel()

			sut := newTestMockClock(t)

			ch := sut.After(0)

			requireFired(t, ch, testStart)
		},
	)

	t.Run("one Advance fires only the waiters due, leaves the rest pending", func(t *testing.T) {
		t.Parallel()

		sut := newTestMockClock(t)
		soon := sut.After(5 * time.Second)
		later := sut.After(20 * time.Second)

		sut.Advance(10 * time.Second)

		requireFired(t, soon, testStart.Add(5*time.Second))
		requireNotFired(t, later)
	})
}

func TestMockClock_ConcurrentAccess(t *testing.T) {
	t.Run("Now, After and Advance are race-safe under concurrent use", func(t *testing.T) {
		sut := newTestMockClock(t)

		const goroutines = 50
		// Now is only ever read concurrently with Advance(time.Millisecond)
		// calls, so every read must land in [testStart, testStart +
		// goroutines*time.Millisecond] regardless of interleaving.
		minNow := testStart
		maxNow := testStart.Add(goroutines * time.Millisecond)

		var wg sync.WaitGroup
		var outOfRange atomic.Int64
		chs := make(chan (<-chan time.Time), goroutines)

		wg.Add(goroutines * 3)
		for i := 0; i < goroutines; i++ {
			go func() {
				defer wg.Done()
				got := sut.Now()
				if got.Before(minNow) || got.After(maxNow) {
					outOfRange.Add(1)
				}
			}()
			go func() {
				defer wg.Done()
				sut.Advance(time.Millisecond)
			}()
			go func(n int) {
				defer wg.Done()
				ch := sut.After(time.Duration(n) * time.Millisecond)
				chs <- ch
			}(i)
		}

		wg.Wait()
		close(chs)

		assert.Zero(t, outOfRange.Load())

		// Flush every possible deadline so no waiter is left pending, then
		// drain non-blockingly: a waiter fired at most once is the
		// property under race, not a specific fired count (registration
		// order versus the concurrent Advances above is not deterministic).
		sut.Advance(goroutines * time.Millisecond)
		for ch := range chs {
			select {
			case <-ch:
			default:
			}
		}
	})
}
