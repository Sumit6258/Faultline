package ratelimit

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLeakyBucket_AllowsUpToCapacityThenBlocks(t *testing.T) {
	lb := NewLeakyBucket(3, 1)
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lb.clock = func() time.Time { return fakeNow }

	for i := 0; i < 3; i++ {
		if !lb.Allow("user1") {
			t.Fatalf("request %d should be allowed, bucket has room", i)
		}
	}
	if lb.Allow("user1") {
		t.Fatal("bucket is full, this request should be blocked")
	}
}

func TestLeakyBucket_LeaksOverTime(t *testing.T) {
	lb := NewLeakyBucket(2, 1) // capacity 2, leaks 1 per second
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lb.clock = func() time.Time { return fakeNow }

	lb.Allow("user1")
	lb.Allow("user1")
	if lb.Allow("user1") {
		t.Fatal("bucket should be full")
	}

	fakeNow = fakeNow.Add(time.Second)
	if !lb.Allow("user1") {
		t.Fatal("one unit should have leaked after one second, making room")
	}
}

func TestLeakyBucket_ConcurrentAccessIsSafe(t *testing.T) {
	lb := NewLeakyBucket(1000, 100)
	var wg sync.WaitGroup
	var allowed atomic.Int32

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if lb.Allow("shared-key") {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 200 {
		t.Fatalf("expected all 200 concurrent requests within capacity to be allowed, got %d", allowed.Load())
	}
}
