package ratelimit

import (
	"testing"
	"time"
)

// boundaryScenario is the traffic shape that separates the algorithms: one
// request to anchor the first window, then limit-1 requests just before
// the first window ends, then limit requests just after it ends.
func boundaryScenario(limit int, admit func(at time.Duration) bool) (accepted int) {
	if admit(0) {
		accepted++
	}
	for i := 0; i < limit-1; i++ {
		if admit(900 * time.Millisecond) {
			accepted++
		}
	}
	for i := 0; i < limit; i++ {
		if admit(1100 * time.Millisecond) {
			accepted++
		}
	}
	return accepted
}

// TestBoundaryBurst measures, with the real implementations, how much each
// algorithm lets through in that scenario with a limit of 4 per second.
// The README quotes these exact numbers.
func TestBoundaryBurst(t *testing.T) {
	const limit = 4
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	fw := NewFixedWindow(limit, time.Second)
	sw := NewSlidingWindowCounter(limit, time.Second)
	tb := NewTokenBucket(limit, limit)
	lb := NewLeakyBucket(limit, limit)
	q := NewLeakyBucketQueue(limit, time.Second/limit)

	var now time.Time
	clock := func() time.Time { return now }
	fw.clock, sw.clock, tb.clock, lb.clock, q.clock = clock, clock, clock, clock, clock

	via := func(l Limiter) func(time.Duration) bool {
		return func(at time.Duration) bool {
			now = base.Add(at)
			return l.Allow("client")
		}
	}

	got := map[string]int{
		"fixed window":         boundaryScenario(limit, via(fw)),
		"sliding window":       boundaryScenario(limit, via(sw)),
		"token bucket":         boundaryScenario(limit, via(tb)),
		"leaky bucket (meter)": boundaryScenario(limit, via(lb)),
		"leaky bucket (queue)": boundaryScenario(limit, func(at time.Duration) bool { now = base.Add(at); _, ok := q.Admit("client"); return ok }),
	}
	want := map[string]int{
		"fixed window":         8,
		"sliding window":       5,
		"token bucket":         5,
		"leaky bucket (meter)": 5,
		"leaky bucket (queue)": 6,
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s: expected %d of 8 requests accepted across the window boundary, got %d", name, w, got[name])
		}
		t.Logf("%-22s accepted %d of 8", name, got[name])
	}
}
