package fetch

import "time"

// Result is the outcome of a single-record fetch at a pointer's offset.
//
// The two flags name the two ways a pointed-at record can be gone, per
// SPEC "Compaction model":
//
//   - Superseded: compaction removed the record because a newer value for
//     the same key exists further down the log and the index has not
//     caught up to it yet. Transient - wait for the checkpoint to advance
//     and re-resolve the pointer.
//   - Evicted: retention dropped the whole segment; the offset is below
//     the log start. Permanent - the value no longer exists anywhere.
//
// The timeline, for a key K pointed at offset 3:
//
//	offset:   0     1     2     3      ...    7
//	        [...] [...] [...] [K=v1]  ...  [K=v2]
//	                            ^ptr
//
//	compaction erases K=v1 (v2 exists)  -> fetch at 3 = Superseded
//	retention drops segments through 3  -> fetch at 3 = Evicted
//	                                       (logStartOffset > ptr.Offset)
//
// The two are mutually exclusive: eviction is decided by the log start
// offset before any record is examined.
type Result struct {
	Value []byte
	// Timestamp is the record's timestamp; zero-value except on a hit.
	Timestamp  time.Time
	Superseded bool
	Evicted    bool
}
