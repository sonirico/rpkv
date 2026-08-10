package fetch

// Result is the outcome of a single-record fetch at a pointer's offset.
type Result struct {
	Value      []byte
	Superseded bool
	Evicted    bool
}
