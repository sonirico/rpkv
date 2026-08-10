package index

// Lookup is the result of Index.Get: the Pointer for the key when Found is
// true, zero value otherwise.
type Lookup struct {
	Pointer Pointer
	Found   bool
}
