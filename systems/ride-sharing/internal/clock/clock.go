// Package clock lets the dispatcher run on real time in production and on a
// time the test controls. Offers expire after seconds and quotes after
// minutes, so tests that sleep would be slow and flaky, and tests that cannot
// control time cannot check expiry at all.
package clock

import (
	"sync"
	"time"
)

type Clock interface{ Now() time.Time }

type Real struct{}

func (Real) Now() time.Time { return time.Now() }

// Fake is a clock that only moves when told to.
type Fake struct {
	mu sync.Mutex
	t  time.Time
}

func NewFake(start time.Time) *Fake { return &Fake{t: start} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	f.t = f.t.Add(d)
	f.mu.Unlock()
}

func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	f.t = t
	f.mu.Unlock()
}
