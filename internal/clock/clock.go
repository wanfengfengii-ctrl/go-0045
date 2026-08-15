// Package clock provides an injectable time source so that expiry scanning and
// command deadlines are deterministic in tests and never depend on the wall
// clock.
package clock

import (
	"sync"
	"time"
)

// Clock abstracts the source of "now" for the whole system. Production code
// uses Real; tests use Fixed and advance it manually so that lease expiry and
// device timeouts fire without sleeping.
type Clock interface {
	// Now returns the current logical time.
	Now() time.Time
}

// Real is a Clock backed by time.Now.
type Real struct{}

// Now returns the wall-clock time.
func (Real) Now() time.Time { return time.Now() }

// Fixed is a deterministic, mutable Clock used in tests and by the recovery
// worker when driven manually. It is safe for concurrent use.
type Fixed struct {
	mu sync.Mutex
	t  time.Time
}

// NewFixed returns a Fixed clock set to t.
func NewFixed(t time.Time) *Fixed { return &Fixed{t: t} }

// Now returns the current logical time.
func (f *Fixed) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

// Set replaces the current logical time.
func (f *Fixed) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = t
}

// Advance moves the logical time forward by d.
func (f *Fixed) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}
