// Package clocktest provides a deterministic clock.Clock for tests, driven
// by Advance instead of the wall clock.
package clocktest

import (
	"sync"
	"time"

	"github.com/sonirico/rpkv/clock"
)

var _ clock.Clock = (*Mock)(nil)

// waiter is a pending After call: it fires once the virtual now reaches or
// passes deadline.
type waiter struct {
	deadline time.Time
	ch       chan time.Time
}

// Mock is a clock.Clock whose notion of "now" only moves when Advance
// is called. After registers a waiter that Advance fires once its deadline
// is reached or passed; a waiter whose deadline has already passed at
// registration time fires immediately, without waiting for a subsequent
// Advance.
type Mock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
	notify  chan<- struct{}
}

// NewMock returns a Mock whose virtual now starts at start.
func NewMock(start time.Time) *Mock {
	return &Mock{now: start}
}

// Now returns the current virtual time.
func (c *Mock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

// After returns a buffered channel (capacity 1) that delivers the virtual
// deadline once Advance moves now to or past it. A deadline already due at
// registration time fires immediately.
func (c *Mock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()

	deadline := c.now.Add(d)
	ch := make(chan time.Time, 1)

	if !deadline.After(c.now) {
		ch <- deadline
		c.mu.Unlock()
		return ch
	}

	c.waiters = append(c.waiters, waiter{deadline: deadline, ch: ch})
	notify := c.notify
	c.mu.Unlock()

	if notify != nil {
		notify <- struct{}{}
	}

	return ch
}

// AfterNotify registers ch to receive one signal each time After parks a
// waiter, sent after the waiter is registered and outside the mutex, so a
// test can Advance only once the waiter is provably parked. The send
// blocks until ch is received from. A nil channel (the default) disables
// notification. After calls whose deadline is already due fire
// immediately and do not notify.
func (c *Mock) AfterNotify(ch chan<- struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.notify = ch
}

// Advance moves the virtual now forward by d and fires every pending
// waiter whose deadline is now due. Firing is non-blocking: After's
// channels are buffered, so a waiter that never reads cannot deadlock
// Advance.
func (c *Mock) Advance(d time.Duration) {
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
