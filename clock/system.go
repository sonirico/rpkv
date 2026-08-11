package clock

import "time"

// system is the production Clock, delegating to the time package.
type system struct{}

// NewSystem returns a Clock backed by the real wall clock.
func NewSystem() Clock {
	return system{}
}

func (system) Now() time.Time {
	return time.Now()
}

func (system) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}
