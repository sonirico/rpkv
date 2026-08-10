package fetch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sonirico/rpkv/index"
)

func TestVerifyFetch(t *testing.T) {
	t.Parallel()

	ptr := index.Pointer{Partition: 0, Offset: 10}
	key := []byte("k1")

	tests := []struct {
		name           string
		records        []*kgo.Record
		logStartOffset int64
		wantResult     Result
		wantResolved   bool
	}{
		{
			name: "exact offset and equal key resolves value",
			records: []*kgo.Record{
				{Key: key, Value: []byte("v1"), Offset: 10},
			},
			wantResult:   Result{Value: []byte("v1")},
			wantResolved: true,
		},
		{
			name: "exact offset and equal key with nil value returns nil value",
			records: []*kgo.Record{
				{Key: key, Value: nil, Offset: 10},
			},
			wantResult:   Result{Value: nil},
			wantResolved: true,
		},
		{
			name: "exact offset with different key is superseded",
			records: []*kgo.Record{
				{Key: []byte("other"), Value: []byte("v1"), Offset: 10},
			},
			wantResult:   Result{Superseded: true},
			wantResolved: true,
		},
		{
			name: "first returned offset beyond pointer is superseded",
			records: []*kgo.Record{
				{Key: key, Value: []byte("v2"), Offset: 11},
			},
			wantResult:   Result{Superseded: true},
			wantResolved: true,
		},
		{
			name:           "log start beyond pointer is evicted",
			records:        nil,
			logStartOffset: 11,
			wantResult:     Result{Evicted: true},
			wantResolved:   true,
		},
		{
			name: "log start beyond pointer wins over records that look superseded",
			records: []*kgo.Record{
				{Key: key, Value: []byte("v2"), Offset: 11},
			},
			logStartOffset: 11,
			wantResult:     Result{Evicted: true},
			wantResolved:   true,
		},
		{
			name:         "empty records is unresolved",
			records:      nil,
			wantResult:   Result{},
			wantResolved: false,
		},
		{
			name: "all offsets below pointer is unresolved",
			records: []*kgo.Record{
				{Key: key, Value: []byte("v0"), Offset: 9},
			},
			wantResult:   Result{},
			wantResolved: false,
		},
		{
			name: "equal key match with later records in the same slice is value, not superseded",
			records: []*kgo.Record{
				{Key: key, Value: []byte("v1"), Offset: 10},
				{Key: key, Value: []byte("v2"), Offset: 11},
			},
			wantResult:   Result{Value: []byte("v1")},
			wantResolved: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotResult, gotResolved := verifyFetch(tc.records, tc.logStartOffset, ptr, key)

			assert.Equal(t, tc.wantResult, gotResult)
			assert.Equal(t, tc.wantResolved, gotResolved)
		})
	}
}
