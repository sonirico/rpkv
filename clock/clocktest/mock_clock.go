// Package clocktest provides a deterministic clock.Clock for tests, driven
// by Advance instead of the wall clock.
package clocktest

import (
	"sync"
	"time"

	"github.com/sonirico/rpkv/clock"
)

var _ clock.Clock = (*MockClock)(nil)

// waiter is a pending After call: it fires once the virtual now reaches or
// passes deadline.
type waiter struct {
	deadline time.Time
	ch       chan time.Time
}

// MockClock is a clock.Clock whose notion of "now" only moves when Advance
// is called. After registers a waiter that Advance fires once its deadline
// is reached or passed; a waiter whose deadline has already passed at
// registration time fires immediately, without waiting for a subsequent
// Advance.
type MockClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
}

// NewMockClock returns a MockClock whose virtual now starts at start.
func NewMockClock(start time.Time) *MockClock {
	return &MockClock{now: start}
}

// Now returns the current virtual time.
func (c *MockClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

// After returns a buffered channel (capacity 1) that delivers the virtual
// deadline once Advance moves now to or past it. A deadline already due at
// registration time fires immediately.
func (c *MockClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	deadline := c.now.Add(d)
	ch := make(chan time.Time, 1)

	if !deadline.After(c.now) {
		ch <- deadline
		return ch
	}

	c.waiters = append(c.waiters, waiter{deadline: deadline, ch: ch})
	return ch
}

// Advance moves the virtual now forward by d and fires every pending
// waiter whose deadline is now due. Firing is non-blocking: After's
// channels are buffered, so a waiter that never reads cannot deadlock
// Advance.
func (c *MockClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)

	remaining := c.waiters[:0]
	for _, w := range c.waiters {
		if !w.deadline.After(c.now) {
			w.ch <- w.deadline
			continue
		}
		remaining = append(remaining, w)
	}
	c.waiters = remaining
}
