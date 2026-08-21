package index

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/cockroachdb/pebble"
)

const (
	ownershipModeAll      byte = 0
	ownershipModeExplicit byte = 1
)

// ErrOwnershipMismatch reports a data directory whose recorded partition
// set differs from the configured one.
var ErrOwnershipMismatch = errors.New("index: ownership mismatch")

// Ownership is the set of partitions a process owns. The zero-value-like
// "all" mode preserves the unsharded behavior.
type Ownership struct {
	All        bool
	Partitions []int32
}

// NewOwnership builds an Ownership from a configured partition list: an
// empty or nil list means all partitions; otherwise the list is sorted
// ascending and deduplicated.
func NewOwnership(partitions []int32) Ownership {
	if len(partitions) == 0 {
		return Ownership{All: true}
	}

	sorted := slices.Clone(partitions)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)

	return Ownership{Partitions: sorted}
}

// Owns reports whether partition belongs to the set.
func (o Ownership) Owns(partition int32) bool {
	if o.All {
		return true
	}
	_, found := slices.BinarySearch(o.Partitions, partition)
	return found
}

// Equal reports whether both sets are the same mode and the same list.
func (o Ownership) Equal(other Ownership) bool {
	return o.All == other.All && slices.Equal(o.Partitions, other.Partitions)
}

// String renders the set for error messages: "all" or "0,3,7".
func (o Ownership) String() string {
	if o.All {
		return "all"
	}

	parts := make([]string, len(o.Partitions))
	for i, p := range o.Partitions {
		parts[i] = strconv.FormatInt(int64(p), 10)
	}
	return strings.Join(parts, ",")
}

// EnsureOwnership guards a data directory against being reattached to the
// wrong shard: it compares the recorded partition set against o and
// refuses on mismatch. A directory with no record but existing
// checkpoints was written by a pre-sharding rpkv, meaning "all"; an empty
// directory adopts o and records it.
func (ix *Index) EnsureOwnership(o Ownership) error {
	val, closer, err := ix.db.Get([]byte{ownershipKeyPrefix})
	if err == nil {
		recorded, decodeErr := decodeOwnership(val)
		closeErr := closer.Close()
		if decodeErr != nil {
			return decodeErr
		}
		if closeErr != nil {
			return fmt.Errorf("index: close ownership value: %w", closeErr)
		}
		return checkOwnership(recorded, o)
	}
	if !errors.Is(err, pebble.ErrNotFound) {
		return fmt.Errorf("index: read ownership: %w", err)
	}

	legacy, err := ix.hasCheckpoints()
	if err != nil {
		return err
	}
	if legacy {
		recorded := NewOwnership(nil)
		if err := checkOwnership(recorded, o); err != nil {
			return err
		}
		return ix.recordOwnership(o)
	}

	return ix.recordOwnership(o)
}

func (ix *Index) hasCheckpoints() (found bool, err error) {
	iter, err := ix.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte{checkpointKeyPrefix},
		UpperBound: []byte{checkpointKeyPrefix + 1},
	})
	if err != nil {
		return false, fmt.Errorf("index: new checkpoint iterator: %w", err)
	}
	defer func() {
		if closeErr := iter.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("index: close checkpoint iterator: %w", closeErr)
		}
	}()

	return iter.First(), nil
}

func (ix *Index) recordOwnership(o Ownership) error {
	if err := ix.db.Set([]byte{ownershipKeyPrefix}, encodeOwnership(o), pebble.Sync); err != nil {
		return fmt.Errorf("index: set ownership: %w", err)
	}
	return nil
}

func checkOwnership(recorded, o Ownership) error {
	if !recorded.Equal(o) {
		return fmt.Errorf(
			"index: data dir owns %s, configured %s: %w",
			recorded,
			o,
			ErrOwnershipMismatch,
		)
	}
	return nil
}

func encodeOwnership(o Ownership) []byte {
	dst := make([]byte, 1+4*len(o.Partitions))
	if o.All {
		dst[0] = ownershipModeAll
		return dst
	}
	dst[0] = ownershipModeExplicit
	for i, p := range o.Partitions {
		off := 1 + 4*i
		binary.BigEndian.PutUint32(dst[off:off+4], uint32(p))
	}
	return dst
}

func decodeOwnership(value []byte) (Ownership, error) {
	if len(value) == 0 || (len(value)-1)%4 != 0 {
		return Ownership{}, fmt.Errorf("index: decode ownership: invalid length %d", len(value))
	}

	switch value[0] {
	case ownershipModeAll:
		return Ownership{All: true}, nil
	case ownershipModeExplicit:
		n := (len(value) - 1) / 4
		partitions := make([]int32, n)
		for i := range partitions {
			off := 1 + 4*i
			partitions[i] = int32(binary.BigEndian.Uint32(value[off : off+4]))
		}
		return Ownership{Partitions: partitions}, nil
	default:
		return Ownership{}, fmt.Errorf("index: decode ownership: unknown mode %d", value[0])
	}
}
