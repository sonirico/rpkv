// Package index maintains the Pebble-backed secondary index key ->
// (partition, offset) and the per-partition applied-offset checkpoints. It
// never stores or caches a record's value; only pointer-sized entries.
package index

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/cockroachdb/pebble"
)

const (
	entryKeyPrefix      byte = 0x01
	checkpointKeyPrefix byte = 0x02
	ownershipKeyPrefix  byte = 0x03

	pointerValueLen  = 4 + 8 // partition int32 BE ++ offset int64 BE
	offsetValueLen   = 8     // checkpoint offset int64 BE
	checkpointKeyLen = 1 + 4 // prefix ++ partition int32 BE
)

// Index is the Pebble-backed key -> Pointer store and per-partition
// checkpoint tracker. It is safe for concurrent use: Pebble itself
// serializes access to the underlying DB and batch.
type Index struct {
	db        *pebble.DB
	closeOnce sync.Once
}

// New wires an already-opened Pebble DB into an Index. It does not
// open or configure the DB - that is the caller's responsibility.
func New(db *pebble.DB) *Index {
	return &Index{db: db}
}

// Apply commits entries and checkpoint advances in a single atomic Pebble
// batch. A Tombstone entry deletes its key. The batch is Sync'd only when
// it carries a checkpoint advance, per the Pebble layout contract.
func (ix *Index) Apply(entries []Entry, checkpoints map[int32]int64) (err error) {
	batch := ix.db.NewBatch()
	defer func() {
		if closeErr := batch.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("index: close batch: %w", closeErr)
		}
	}()

	var keyBuf []byte
	var pointerBuf [pointerValueLen]byte
	for _, e := range entries {
		keyBuf = appendEntryKey(keyBuf[:0], e.Key)
		if e.Tombstone {
			if err := batch.Delete(keyBuf, nil); err != nil {
				return fmt.Errorf("index: delete key: %w", err)
			}
			continue
		}
		encodePointer(pointerBuf[:], e.Pointer)
		if err := batch.Set(keyBuf, pointerBuf[:], nil); err != nil {
			return fmt.Errorf("index: set key: %w", err)
		}
	}

	var checkpointKeyBuf [checkpointKeyLen]byte
	var offsetBuf [offsetValueLen]byte
	for partition, offset := range checkpoints {
		encodeCheckpointKey(checkpointKeyBuf[:], partition)
		encodeOffset(offsetBuf[:], offset)
		if err := batch.Set(checkpointKeyBuf[:], offsetBuf[:], nil); err != nil {
			return fmt.Errorf("index: set checkpoint: %w", err)
		}
	}

	writeOpts := pebble.NoSync
	if len(checkpoints) > 0 {
		writeOpts = pebble.Sync
	}
	if err := batch.Commit(writeOpts); err != nil {
		return fmt.Errorf("index: commit batch: %w", err)
	}
	return nil
}

// Get looks up the Pointer for key. Lookup.Found is false when the key is
// absent from the index.
func (ix *Index) Get(key []byte) (Lookup, error) {
	keyBuf := appendEntryKey(make([]byte, 0, len(key)+1), key)

	var raw [pointerValueLen]byte
	found, err := ix.lookupFixed(keyBuf, raw[:])
	if err != nil {
		return Lookup{}, err
	}
	if !found {
		return Lookup{}, nil
	}
	return Lookup{Pointer: decodePointer(raw[:]), Found: true}, nil
}

// Checkpoint returns the highest offset applied for partition, or -1 when
// no checkpoint has been recorded for it.
func (ix *Index) Checkpoint(partition int32) (int64, error) {
	var keyBuf [checkpointKeyLen]byte
	encodeCheckpointKey(keyBuf[:], partition)

	var raw [offsetValueLen]byte
	found, err := ix.lookupFixed(keyBuf[:], raw[:])
	if err != nil {
		return -1, err
	}
	if !found {
		return -1, nil
	}
	return decodeOffset(raw[:]), nil
}

// Close closes the underlying Pebble DB. Idempotent.
func (ix *Index) Close() error {
	var err error
	ix.closeOnce.Do(func() {
		err = ix.db.Close()
	})
	return err
}

// lookupFixed fetches key and copies its value into dst, which must be
// exactly the expected value length for the caller's encoding. The copy
// happens before the Pebble Closer is released, since the fetched slice is
// only valid until then.
func (ix *Index) lookupFixed(key []byte, dst []byte) (found bool, err error) {
	val, closer, getErr := ix.db.Get(key)
	if getErr != nil {
		if errors.Is(getErr, pebble.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("index: get: %w", getErr)
	}
	if len(val) != len(dst) {
		closeErr := closer.Close()
		return false, fmt.Errorf(
			"index: corrupt value: got %d bytes, want %d bytes (close: %v)",
			len(val),
			len(dst),
			closeErr,
		)
	}
	copy(dst, val)
	if closeErr := closer.Close(); closeErr != nil {
		return false, fmt.Errorf("index: close value: %w", closeErr)
	}
	return true, nil
}

func appendEntryKey(dst, key []byte) []byte {
	dst = append(dst, entryKeyPrefix)
	dst = append(dst, key...)
	return dst
}

func encodeCheckpointKey(dst []byte, partition int32) {
	dst[0] = checkpointKeyPrefix
	binary.BigEndian.PutUint32(dst[1:checkpointKeyLen], uint32(partition))
}

func encodePointer(dst []byte, p Pointer) {
	binary.BigEndian.PutUint32(dst[0:4], uint32(p.Partition))
	binary.BigEndian.PutUint64(dst[4:pointerValueLen], uint64(p.Offset))
}

func decodePointer(b []byte) Pointer {
	return Pointer{
		Partition: int32(binary.BigEndian.Uint32(b[0:4])),
		Offset:    int64(binary.BigEndian.Uint64(b[4:pointerValueLen])),
	}
}

func encodeOffset(dst []byte, offset int64) {
	binary.BigEndian.PutUint64(dst, uint64(offset))
}

func decodeOffset(b []byte) int64 {
	return int64(binary.BigEndian.Uint64(b))
}
