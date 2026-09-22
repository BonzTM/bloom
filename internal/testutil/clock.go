// Package testutil holds small, dependency-free helpers shared across the
// module's tests. It is internal and imports only the standard library; it
// must never be imported by production code.
package testutil

import (
	"sync"
	"time"
)

// FakeClock is a controllable, concurrency-safe core.Clock implementation for
// tests. It satisfies the core.Clock seam (Now() time.Time) structurally, so it
// can be passed wherever production wires a real clock. Time never advances on
// its own: tests move it explicitly with Set or Advance, which keeps
// time-dependent behavior deterministic.
//
// The zero value is usable and reports the zero time; prefer NewFakeClock to
// start from a known instant.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewFakeClock returns a FakeClock anchored at t. The instant is stored as-is;
// callers that assert on UTC should pass a UTC time (e.g. t.UTC()).
func NewFakeClock(t time.Time) *FakeClock {
	return &FakeClock{now: t}
}

// Now returns the clock's current instant. It is safe for concurrent use.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set moves the clock to t. It is safe for concurrent use.
func (c *FakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// Advance moves the clock forward by d (use a negative d to move backward). It
// is safe for concurrent use.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
