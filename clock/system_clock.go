package clock

import "time"

// systemClock is the production Clock, delegating to the time package.
type systemClock struct{}

// NewSystemClock returns a Clock backed by the real wall clock.
func NewSystemClock() Clock {
	return systemClock{}
}

func (systemClock) Now() time.Time {
	return time.Now()
}

func (systemClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}
