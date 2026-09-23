package ratelimit

import (
	"sync"
	"time"
)

// FixedWindow allows at most limit requests per key inside each fixed size
// window. It is the cheapest algorithm to implement and the easiest to get
// wrong: a client can send limit requests right at the end of one window
// and limit more right at the start of the next, doubling the effective
// rate for a brief moment. See the README for a measured example, and
// SlidingWindowCounter for a fix.
//
// This reference implementation never evicts idle keys. A production
// version would need a TTL sweep or a bounded cache, or the counter map
// grows without bound.
type FixedWindow struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	counters map[string]*fixedWindowState
	clock    Clock
}

type fixedWindowState struct {
	count       int
	windowStart time.Time
}

func NewFixedWindow(limit int, window time.Duration) *FixedWindow {
	return &FixedWindow{
		limit:    limit,
		window:   window,
		counters: make(map[string]*fixedWindowState),
		clock:    realClock,
	}
}

func (f *FixedWindow) Allow(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := f.clock()
	st, ok := f.counters[key]
	if !ok || now.Sub(st.windowStart) >= f.window {
		f.counters[key] = &fixedWindowState{count: 1, windowStart: now}
		return true
	}
	if st.count >= f.limit {
		return false
	}
	st.count++
	return true
}
