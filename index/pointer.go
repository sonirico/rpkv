package index

// Pointer locates a key's latest record in the log: the partition it was
// produced to and its offset within that partition.
type Pointer struct {
	Partition int32
	Offset    int64
}
