package metrics_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonirico/rpkv/metrics"
)

func TestNewNoopHistogram(t *testing.T) {
	t.Run("Observe discards the observation without panicking", func(t *testing.T) {
		t.Parallel()

		sut := metrics.NewNoopHistogram()

		assert.NotPanics(t, func() {
			sut.Observe(1)
		})
	})
}
