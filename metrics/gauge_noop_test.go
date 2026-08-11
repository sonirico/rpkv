package metrics_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonirico/rpkv/metrics"
)

func TestNewNoopGauge(t *testing.T) {
	t.Run("Set discards the observation without panicking", func(t *testing.T) {
		t.Parallel()

		sut := metrics.NewNoopGauge()

		assert.NotPanics(t, func() {
			sut.Set(42)
		})
	})
}
