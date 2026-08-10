package fetch

import (
	"bytes"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sonirico/rpkv/index"
)

// verifyFetch applies the read verification protocol to one polled
// partition. resolved=false means the poll carried nothing decisive
// for ptr (no records for the partition yet) and the caller polls on.
func verifyFetch(
	records []*kgo.Record,
	logStartOffset int64,
	ptr index.Pointer,
	key []byte,
) (res Result, resolved bool) {
	if logStartOffset > ptr.Offset {
		return Result{Evicted: true}, true
	}

	for _, r := range records {
		if r.Offset != ptr.Offset {
			continue
		}
		if bytes.Equal(r.Key, key) {
			return Result{Value: r.Value}, true
		}
		return Result{Superseded: true}, true
	}

	for _, r := range records {
		if r.Offset > ptr.Offset {
			return Result{Superseded: true}, true
		}
	}

	return Result{}, false
}
