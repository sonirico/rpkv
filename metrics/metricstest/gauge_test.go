package metricstest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonirico/rpkv/metrics/metricstest"
)

func TestGauge(t *testing.T) {
	t.Run("Set overwrites, last write wins", func(t *testing.T) {
		t.Parallel()

		type testCase struct {
			name   string
			values []float64
			want   float64
		}
		tests := []testCase{
			{name: "single set", values: []float64{42}, want: 42},
			{name: "multiple sets", values: []float64{1, 2, 3}, want: 3},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				sut := metricstest.NewGauge()

				for _, v := range tc.values {
					sut.Set(v)
				}

				assert.Equal(t, tc.want, sut.Value())
			})
		}
	})
}
