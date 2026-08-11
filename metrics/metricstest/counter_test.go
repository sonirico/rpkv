package metricstest_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonirico/rpkv/metrics/metricstest"
)

func TestCounter(t *testing.T) {
	t.Run("Inc accumulates 1 per call", func(t *testing.T) {
		t.Parallel()

		sut := metricstest.NewCounter()

		sut.Inc()
		sut.Inc()
		sut.Inc()

		assert.Equal(t, float64(3), sut.Count())
	})

	t.Run("Add accumulates deltas", func(t *testing.T) {
		t.Parallel()

		type testCase struct {
			name   string
			deltas []float64
			want   float64
		}
		tests := []testCase{
			{name: "single delta", deltas: []float64{2.5}, want: 2.5},
			{name: "multiple deltas", deltas: []float64{1, 2.5, -1}, want: 2.5},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				sut := metricstest.NewCounter()

				for _, d := range tc.deltas {
					sut.Add(d)
				}

				assert.Equal(t, tc.want, sut.Count())
			})
		}
	})

	t.Run("concurrent Inc from goroutines sums correctly", func(t *testing.T) {
		t.Parallel()

		sut := metricstest.NewCounter()
		const goroutines = 50

		var wg sync.WaitGroup
		wg.Add(goroutines)
		for i := 0; i < goroutines; i++ {
			go func() {
				defer wg.Done()
				sut.Inc()
			}()
		}
		wg.Wait()

		assert.Equal(t, float64(goroutines), sut.Count())
	})
}
