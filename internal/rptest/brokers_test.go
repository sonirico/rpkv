package rptest

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrokers(t *testing.T) {
	type testCase struct {
		name  string
		setup func(t *testing.T)
		want  string
	}

	// setProvisionedAddr simulates Main having provisioned a broker,
	// restoring the package state afterwards.
	setProvisionedAddr := func(t *testing.T, addr string) {
		t.Helper()

		previous := brokerAddr
		brokerAddr = addr
		t.Cleanup(func() {
			brokerAddr = previous
		})
	}

	tests := []testCase{
		{
			name: "env set to a real value overrides the provisioned address",
			setup: func(t *testing.T) {
				setProvisionedAddr(t, "provisioned:9092")
				t.Setenv(brokersEnvVar, "example.com:9092")
			},
			want: "example.com:9092",
		},
		{
			name: "env set to the empty string falls back to the provisioned address",
			setup: func(t *testing.T) {
				setProvisionedAddr(t, "provisioned:9092")
				t.Setenv(brokersEnvVar, "")
			},
			want: "provisioned:9092",
		},
		{
			name: "env genuinely unset falls back to the provisioned address",
			setup: func(t *testing.T) {
				setProvisionedAddr(t, "provisioned:9092")
				// t.Setenv has no unset counterpart: set it first so
				// testing registers the original value for restoration,
				// then unset it for real.
				t.Setenv(brokersEnvVar, "placeholder")
				require.NoError(t, os.Unsetenv(brokersEnvVar))
			},
			want: "provisioned:9092",
		},
		{
			name: "nothing provisioned and no env yields the empty address",
			setup: func(t *testing.T) {
				setProvisionedAddr(t, "")
				t.Setenv(brokersEnvVar, "placeholder")
				require.NoError(t, os.Unsetenv(brokersEnvVar))
			},
			want: "",
		},
	}

	// t.Setenv is incompatible with t.Parallel, so these subtests run
	// sequentially.
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)

			got := Brokers()

			assert.Equal(t, tc.want, got)
		})
	}
}
