package metricstest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonirico/rpkv/metrics/metricstest"
)

func TestHistogram(t *testing.T) {
	t.Run("Observe appends in order", func(t *testing.T) {
		t.Parallel()

		sut := metricstest.NewHistogram()

		sut.Observe(1)
		sut.Observe(2)
		sut.Observe(3)

		assert.Equal(t, []float64{1, 2, 3}, sut.Observations())
	})

	t.Run("Observations returns a copy", func(t *testing.T) {
		t.Parallel()

		sut := metricstest.NewHistogram()
		sut.Observe(1)
		sut.Observe(2)

		got := sut.Observations()
		got[0] = 999

		assert.Equal(t, []float64{1, 2}, sut.Observations())
	})
}
