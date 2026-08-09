// Package clock abstracts time so production code never calls the time
// package directly. clock is the sole production caller of time.Now/After;
// everywhere else depends on the Clock interface and swaps in a mock clock
// for tests.
package clock

import "time"

// Clock provides the time operations rpkv needs. Production code depends on
// this interface, never on the time package directly, so tests can swap in
// a deterministic mock.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// After returns a channel that delivers the current time once the
	// duration d has elapsed.
	After(d time.Duration) <-chan time.Time
}
