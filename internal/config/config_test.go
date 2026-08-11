package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonirico/rpkv/internal/config"
)

func TestParse(t *testing.T) {
	type testCase struct {
		name    string
		args    []string
		env     map[string]string
		want    config.Config
		wantErr bool
	}

	tests := []testCase{
		{
			name: "flags only",
			args: []string{"--brokers", "a:9092,b:9092", "--topics", "t1,t2"},
			want: config.Config{
				Brokers: []string{"a:9092", "b:9092"},
				Topics:  []string{"t1", "t2"},
				DataDir: "./rpkv-data",
				Listen:  ":8080",
			},
		},
		{
			name: "env fallback",
			args: nil,
			env: map[string]string{
				"RPKV_BROKERS":  "c:9092",
				"RPKV_TOPICS":   "t3",
				"RPKV_DATA_DIR": "/tmp/x",
				"RPKV_LISTEN":   ":9999",
			},
			want: config.Config{
				Brokers: []string{"c:9092"},
				Topics:  []string{"t3"},
				DataDir: "/tmp/x",
				Listen:  ":9999",
			},
		},
		{
			name: "flag wins over env",
			args: []string{"--brokers", "flag:9092", "--topics", "t"},
			env: map[string]string{
				"RPKV_BROKERS": "env:9092",
			},
			want: config.Config{
				Brokers: []string{"flag:9092"},
				Topics:  []string{"t"},
				DataDir: "./rpkv-data",
				Listen:  ":8080",
			},
		},
		{
			name:    "missing brokers is an error",
			args:    []string{"--topics", "t"},
			wantErr: true,
		},
		{
			name:    "missing topics is an error",
			args:    []string{"--brokers", "a:9092"},
			wantErr: true,
		},
		{
			name: "whitespace and empty segments dropped",
			args: []string{"--brokers", " a:9092, ,b:9092 ", "--topics", "t"},
			want: config.Config{
				Brokers: []string{"a:9092", "b:9092"},
				Topics:  []string{"t"},
				DataDir: "./rpkv-data",
				Listen:  ":8080",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			got, err := config.Parse(tc.args)

			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
