package rptest_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonirico/rpkv/internal/rptest"
)

const brokersEnvVar = "RPKV_TEST_BROKERS"

func TestBrokers(t *testing.T) {
	type testCase struct {
		name  string
		setup func(t *testing.T)
		want  string
	}

	tests := []testCase{
		{
			name: "set to a real value overrides the default",
			setup: func(t *testing.T) {
				t.Setenv(brokersEnvVar, "example.com:9092")
			},
			want: "example.com:9092",
		},
		{
			name: "set to the empty string falls back to the dev loop default",
			setup: func(t *testing.T) {
				t.Setenv(brokersEnvVar, "")
			},
			want: "localhost:19092",
		},
		{
			name: "genuinely unset falls back to the dev loop default",
			setup: func(t *testing.T) {
				// t.Setenv has no unset counterpart: set it first so
				// testing registers the original value for restoration,
				// then unset it for real.
				t.Setenv(brokersEnvVar, "placeholder")
				require.NoError(t, os.Unsetenv(brokersEnvVar))
			},
			want: "localhost:19092",
		},
	}

	// t.Setenv is incompatible with t.Parallel, so these subtests run
	// sequentially.
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)

			got := rptest.Brokers()

			assert.Equal(t, tc.want, got)
		})
	}
}
