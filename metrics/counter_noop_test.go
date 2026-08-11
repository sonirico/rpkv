package metrics_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonirico/rpkv/metrics"
)

func TestNewNoopCounter(t *testing.T) {
	t.Run("Inc discards the observation without panicking", func(t *testing.T) {
		t.Parallel()

		sut := metrics.NewNoopCounter()

		assert.NotPanics(t, func() {
			sut.Inc()
		})
	})

	t.Run("Add discards the observation without panicking", func(t *testing.T) {
		t.Parallel()

		sut := metrics.NewNoopCounter()

		assert.NotPanics(t, func() {
			sut.Add(2.5)
		})
	})
}
