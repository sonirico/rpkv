package index

// Entry is one key's outcome from applying a record to the index: either a
// new Pointer for the key, or a Tombstone that deletes it.
type Entry struct {
	Key       []byte
	Pointer   Pointer
	Tombstone bool
}
