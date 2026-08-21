package config_test

import (
	"testing"
	"time"

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
				Mode:            config.ModeIndex,
				Brokers:         []string{"a:9092", "b:9092"},
				Topics:          []string{"t1", "t2"},
				DataDir:         "./rpkv-data",
				Listen:          ":8080",
				MetadataRefresh: 30 * time.Second,
				Shards:          []string{},
			},
		},
		{
			name: "env fallback",
			args: nil,
			env: map[string]string{
				"RPKV_BROKERS":          "c:9092",
				"RPKV_TOPICS":           "t3",
				"RPKV_DATA_DIR":         "/tmp/x",
				"RPKV_LISTEN":           ":9999",
				"RPKV_METADATA_REFRESH": "1m",
			},
			want: config.Config{
				Mode:            config.ModeIndex,
				Brokers:         []string{"c:9092"},
				Topics:          []string{"t3"},
				DataDir:         "/tmp/x",
				Listen:          ":9999",
				MetadataRefresh: time.Minute,
				Shards:          []string{},
			},
		},
		{
			name: "flag wins over env",
			args: []string{"--brokers", "flag:9092", "--topics", "t"},
			env: map[string]string{
				"RPKV_BROKERS": "env:9092",
			},
			want: config.Config{
				Mode:            config.ModeIndex,
				Brokers:         []string{"flag:9092"},
				Topics:          []string{"t"},
				DataDir:         "./rpkv-data",
				Listen:          ":8080",
				MetadataRefresh: 30 * time.Second,
				Shards:          []string{},
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
				Mode:            config.ModeIndex,
				Brokers:         []string{"a:9092", "b:9092"},
				Topics:          []string{"t"},
				DataDir:         "./rpkv-data",
				Listen:          ":8080",
				MetadataRefresh: 30 * time.Second,
				Shards:          []string{},
			},
		},
		{
			name: "invalid metadata-refresh is an error",
			args: []string{
				"--brokers",
				"a:9092",
				"--topics",
				"t",
				"--metadata-refresh",
				"notaduration",
			},
			wantErr: true,
		},
		{
			name:    "non-positive metadata-refresh is an error",
			args:    []string{"--brokers", "a:9092", "--topics", "t", "--metadata-refresh", "0s"},
			wantErr: true,
		},
		{
			name: "valid partitions list",
			args: []string{"--brokers", "a:9092", "--topics", "t", "--partitions", "0,2"},
			want: config.Config{
				Mode:            config.ModeIndex,
				Brokers:         []string{"a:9092"},
				Topics:          []string{"t"},
				DataDir:         "./rpkv-data",
				Listen:          ":8080",
				MetadataRefresh: 30 * time.Second,
				Partitions:      []int32{0, 2},
				Shards:          []string{},
			},
		},
		{
			name: "partitions list with whitespace",
			args: []string{"--brokers", "a:9092", "--topics", "t", "--partitions", " 1 , 3 "},
			want: config.Config{
				Mode:            config.ModeIndex,
				Brokers:         []string{"a:9092"},
				Topics:          []string{"t"},
				DataDir:         "./rpkv-data",
				Listen:          ":8080",
				MetadataRefresh: 30 * time.Second,
				Partitions:      []int32{1, 3},
				Shards:          []string{},
			},
		},
		{
			name:    "negative partition is an error",
			args:    []string{"--brokers", "a:9092", "--topics", "t", "--partitions", "-1"},
			wantErr: true,
		},
		{
			name:    "non-numeric partition is an error",
			args:    []string{"--brokers", "a:9092", "--topics", "t", "--partitions", "x"},
			wantErr: true,
		},
		{
			name: "partitions and partition-from-ordinal are mutually exclusive",
			args: []string{
				"--brokers",
				"a:9092",
				"--topics",
				"t",
				"--partitions",
				"0",
				"--partition-from-ordinal",
				"true",
			},
			wantErr: true,
		},
		{
			name: "partition-from-ordinal derives the partition from the hostname",
			args: []string{
				"--brokers",
				"a:9092",
				"--topics",
				"t",
				"--partition-from-ordinal",
				"true",
			},
			env: map[string]string{"HOSTNAME": "rpkv-3"},
			want: config.Config{
				Mode:            config.ModeIndex,
				Brokers:         []string{"a:9092"},
				Topics:          []string{"t"},
				DataDir:         "./rpkv-data",
				Listen:          ":8080",
				MetadataRefresh: 30 * time.Second,
				Partitions:      []int32{3},
				Shards:          []string{},
			},
		},
		{
			name: "hostname without an ordinal suffix is an error",
			args: []string{
				"--brokers",
				"a:9092",
				"--topics",
				"t",
				"--partition-from-ordinal",
				"true",
			},
			env:     map[string]string{"HOSTNAME": "rpkv"},
			wantErr: true,
		},
		{
			name: "hostname with a non-numeric suffix is an error",
			args: []string{
				"--brokers",
				"a:9092",
				"--topics",
				"t",
				"--partition-from-ordinal",
				"true",
			},
			env:     map[string]string{"HOSTNAME": "rpkv-abc"},
			wantErr: true,
		},
		{
			name:    "non-boolean partition-from-ordinal env is an error",
			args:    []string{"--brokers", "a:9092", "--topics", "t"},
			env:     map[string]string{"RPKV_PARTITION_FROM_ORDINAL": "notabool"},
			wantErr: true,
		},
		{
			name: "neither partitions nor partition-from-ordinal set leaves Partitions nil",
			args: []string{"--brokers", "a:9092", "--topics", "t"},
			want: config.Config{
				Mode:            config.ModeIndex,
				Brokers:         []string{"a:9092"},
				Topics:          []string{"t"},
				DataDir:         "./rpkv-data",
				Listen:          ":8080",
				MetadataRefresh: 30 * time.Second,
				Partitions:      nil,
				Shards:          []string{},
			},
		},
		{
			name: "no mode flag defaults to index with empty shards",
			args: []string{"--brokers", "a:9092", "--topics", "t"},
			want: config.Config{
				Mode:            config.ModeIndex,
				Brokers:         []string{"a:9092"},
				Topics:          []string{"t"},
				DataDir:         "./rpkv-data",
				Listen:          ":8080",
				MetadataRefresh: 30 * time.Second,
				Shards:          []string{},
			},
		},
		{
			name: "valid router mode does not require brokers or topics",
			args: []string{"--mode", "router", "--shards", "a:1,b:2"},
			want: config.Config{
				Mode:            config.ModeRouter,
				Brokers:         []string{},
				Topics:          []string{},
				DataDir:         "./rpkv-data",
				Listen:          ":8080",
				MetadataRefresh: 30 * time.Second,
				Shards:          []string{"a:1", "b:2"},
			},
		},
		{
			name:    "router mode without shards is an error",
			args:    []string{"--mode", "router"},
			wantErr: true,
		},
		{
			name:    "bogus mode is an error",
			args:    []string{"--mode", "bogus"},
			wantErr: true,
		},
		{
			name: "shards with index mode is an error",
			args: []string{
				"--brokers",
				"a:9092",
				"--topics",
				"t",
				"--shards",
				"a:1",
			},
			wantErr: true,
		},
		{
			name: "partitions with router mode is an error",
			args: []string{
				"--mode",
				"router",
				"--shards",
				"a:1",
				"--partitions",
				"0",
			},
			wantErr: true,
		},
		{
			name: "partition-from-ordinal with router mode is an error",
			args: []string{
				"--mode",
				"router",
				"--shards",
				"a:1",
				"--partition-from-ordinal",
				"true",
			},
			wantErr: true,
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
