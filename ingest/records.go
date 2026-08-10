package ingest

import (
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sonirico/rpkv/index"
)

// collectEntries converts one partition's fetched records, in offset
// order, into index entries. Null-key records are skipped and counted; a
// nil value is a tombstone.
func collectEntries(records []*kgo.Record) (entries []index.Entry, skipped int64) {
	entries = make([]index.Entry, 0, len(records))
	for _, r := range records {
		if r.Key == nil {
			skipped++
			continue
		}
		entries = append(entries, index.Entry{
			Key:       r.Key,
			Pointer:   index.Pointer{Partition: r.Partition, Offset: r.Offset},
			Tombstone: r.Value == nil,
		})
	}
	return entries, skipped
}
