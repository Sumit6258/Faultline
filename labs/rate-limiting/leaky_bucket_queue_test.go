package ratelimit

import (
	"testing"
	"time"
)

func newTestQueue(capacity int, interval time.Duration, now *time.Time) *LeakyBucketQueue {
	q := NewLeakyBucketQueue(capacity, interval)
	q.clock = func() time.Time { return *now }
	return q
}

func TestLeakyBucketQueue_BurstIsSpacedOutAndOverflowDropped(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q := newTestQueue(3, 100*time.Millisecond, &now)

	var waits []time.Duration
	dropped := 0
	for i := 0; i < 10; i++ {
		wait, ok := q.Admit("client")
		if !ok {
			dropped++
			continue
		}
		waits = append(waits, wait)
	}

	want := []time.Duration{0, 100 * time.Millisecond, 200 * time.Millisecond, 300 * time.Millisecond}
	if len(waits) != len(want) {
		t.Fatalf("expected %d of 10 instantaneous requests to be accepted (one released now plus capacity 3 waiting), got %d: %v", len(want), len(waits), waits)
	}
	for i := range want {
		if waits[i] != want[i] {
			t.Fatalf("request %d: expected to wait %s, got %s", i, want[i], waits[i])
		}
	}
	if dropped != 6 {
		t.Fatalf("expected 6 requests dropped, got %d", dropped)
	}
}

func TestLeakyBucketQueue_ArrivalsAtTheReleaseRateNeverWait(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q := newTestQueue(2, 100*time.Millisecond, &now)

	for i := 0; i < 20; i++ {
		wait, ok := q.Admit("client")
		if !ok || wait != 0 {
			t.Fatalf("arrival %d at exactly the release rate should pass with no wait, got wait=%s ok=%v", i, wait, ok)
		}
		now = now.Add(100 * time.Millisecond)
	}
}

func TestLeakyBucketQueue_RecoversAfterIdle(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q := newTestQueue(1, time.Second, &now)

	q.Admit("client")
	q.Admit("client") // queued, released one second from now
	if _, ok := q.Admit("client"); ok {
		t.Fatal("the queue holds one waiting request, a third instantaneous request should be dropped")
	}

	now = now.Add(10 * time.Second)
	wait, ok := q.Admit("client")
	if !ok || wait != 0 {
		t.Fatalf("after a long idle period a request should be released immediately, got wait=%s ok=%v", wait, ok)
	}
}

func TestLeakyBucketQueue_SustainedOverloadIsBoundedByCapacity(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	interval := 100 * time.Millisecond
	q := newTestQueue(3, interval, &now)

	var maxWait time.Duration
	dropped := 0
	for i := 0; i < 200; i++ { // arrivals every 50ms, twice the release rate
		wait, ok := q.Admit("client")
		if !ok {
			dropped++
		} else if wait > maxWait {
			maxWait = wait
		}
		now = now.Add(50 * time.Millisecond)
	}

	if dropped == 0 {
		t.Fatal("arrivals at twice the release rate should eventually be dropped")
	}
	if limit := time.Duration(3) * interval; maxWait > limit {
		t.Fatalf("no accepted request should ever wait longer than capacity*interval (%s), got %s", limit, maxWait)
	}
}
