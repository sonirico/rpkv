package metrics

var _ Counter = noopCounter{}

// noopCounter is a Counter that discards every observation.
type noopCounter struct{}

// NewNoopCounter returns a Counter that discards every observation.
func NewNoopCounter() Counter {
	return noopCounter{}
}

func (noopCounter) Inc() {}

func (noopCounter) Add(delta float64) {}
