package rptest

import (
	"bytes"
	"testing"

	"github.com/sonirico/vago/testit"
	"github.com/stretchr/testify/assert"
)

func TestWriterLogger(t *testing.T) {
	type testCase struct {
		name string
		log  func(l testit.Logger)
		want string
	}

	tests := []testCase{
		{
			name: "Info joins args and terminates the line",
			log: func(l testit.Logger) {
				l.Info("resource", "down")
			},
			want: "resource down\n",
		},
		{
			name: "Infof formats and terminates the line",
			log: func(l testit.Logger) {
				l.Infof("stopping resource: %s", "redpanda")
			},
			want: "stopping resource: redpanda\n",
		},
		{
			name: "Errorf formats and terminates the line",
			log: func(l testit.Logger) {
				l.Errorf("could not purge resource: %s", "gone")
			},
			want: "could not purge resource: gone\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			logger := newWriterLogger(&buf)

			tc.log(logger)

			assert.Equal(t, tc.want, buf.String())
		})
	}
}
