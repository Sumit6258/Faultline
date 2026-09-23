package ratelimit

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSlidingWindow_AllowsUpToLimitThenBlocks(t *testing.T) {
	sw := NewSlidingWindowCounter(3, time.Second)
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sw.clock = func() time.Time { return fakeNow }

	for i := 0; i < 3; i++ {
		if !sw.Allow("user1") {
			t.Fatalf("request %d should be allowed", i)
		}
	}
	if sw.Allow("user1") {
		t.Fatal("4th request in the same window should be blocked")
	}
}

func TestSlidingWindow_SmoothsBoundaryBurst(t *testing.T) {
	// FixedWindow lets a client send `limit` requests right before a window
	// boundary and `limit` more right after, for close to 2x the limit in a
	// short span. SlidingWindowCounter should not allow that.
	limit := 4
	window := time.Second
	sw := NewSlidingWindowCounter(limit, window)
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 900*int(time.Millisecond), time.UTC)
	sw.clock = func() time.Time { return fakeNow }

	allowedFirstBurst := 0
	for i := 0; i < limit; i++ {
		if sw.Allow("user1") {
			allowedFirstBurst++
		}
	}
	if allowedFirstBurst != limit {
		t.Fatalf("expected all %d requests before the boundary to be allowed, got %d", limit, allowedFirstBurst)
	}

	fakeNow = fakeNow.Add(200 * time.Millisecond)
	allowedSecondBurst := 0
	for i := 0; i < limit; i++ {
		if sw.Allow("user1") {
			allowedSecondBurst++
		}
	}
	if allowedSecondBurst >= limit {
		t.Fatalf("sliding window should have blocked most of the second burst since it overlaps the first, got %d of %d allowed", allowedSecondBurst, limit)
	}
	t.Logf("fixed window would have allowed %d total across the boundary, sliding window allowed %d", limit*2, limit+allowedSecondBurst)
}

func TestSlidingWindow_ConcurrentAccessIsSafe(t *testing.T) {
	sw := NewSlidingWindowCounter(1000, time.Minute)
	var wg sync.WaitGroup
	var allowed atomic.Int32

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if sw.Allow("shared-key") {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 200 {
		t.Fatalf("expected all 200 concurrent requests within the limit to be allowed, got %d", allowed.Load())
	}
}
