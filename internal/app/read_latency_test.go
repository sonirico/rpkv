package app_test

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// percentile returns the nearest-rank q-th percentile (0-100) of a sorted
// samples slice.
func percentile(sorted []time.Duration, q float64) time.Duration {
	idx := int(math.Ceil(q/100*float64(len(sorted)))) - 1
	return sorted[idx]
}

func toMillis(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

// tenSortedSamples is 1ms..10ms ascending, the fixture shared by every
// multi-sample TestPercentile case.
func tenSortedSamples() []time.Duration {
	out := make([]time.Duration, 10)
	for i := range out {
		out[i] = time.Duration(i+1) * time.Millisecond
	}
	return out
}

// TestPercentile locks down the nearest-rank formula documented on
// percentile's doc comment (internal/app/read_latency_integration_test.go)
// against known inputs. It runs without the integration tag and without a
// broker: percentile is a pure function over an already-sorted slice, so
// TestReadLatencyBenchmark's own require.Positive/require.GreaterOrEqual
// checks on real wall-clock samples can't distinguish a correct nearest-rank
// index from an off-by-one one - only a fixed, known input can.
func TestPercentile(t *testing.T) {
	tests := []struct {
		name    string
		samples []time.Duration
		q       float64
		want    time.Duration
	}{
		{
			name:    "p50 of ten samples is the 5th value",
			samples: tenSortedSamples(),
			q:       50,
			want:    5 * time.Millisecond,
		},
		{
			name:    "p90 of ten samples is the 9th value",
			samples: tenSortedSamples(),
			q:       90,
			want:    9 * time.Millisecond,
		},
		{
			name:    "p99 of ten samples rounds up to the 10th value",
			samples: tenSortedSamples(),
			q:       99,
			want:    10 * time.Millisecond,
		},
		{
			name:    "p100 of ten samples is the last value",
			samples: tenSortedSamples(),
			q:       100,
			want:    10 * time.Millisecond,
		},
		{
			name:    "single-sample slice returns that sample regardless of q",
			samples: []time.Duration{3 * time.Millisecond},
			q:       1,
			want:    3 * time.Millisecond,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := percentile(tc.samples, tc.q)

			require.Equal(t, tc.want, got)
		})
	}
}

func TestToMillis(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want float64
	}{
		{name: "whole millisecond", d: 2 * time.Millisecond, want: 2},
		{name: "fractional millisecond", d: 1500 * time.Microsecond, want: 1.5},
		{name: "zero", d: 0, want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := toMillis(tc.d)

			require.InDelta(t, tc.want, got, 1e-9)
		})
	}
}
