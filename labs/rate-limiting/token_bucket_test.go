package ratelimit

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTokenBucket_AllowsBurstUpToCapacity(t *testing.T) {
	tb := NewTokenBucket(5, 1)
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tb.clock = func() time.Time { return fakeNow }

	for i := 0; i < 5; i++ {
		if !tb.Allow("user1") {
			t.Fatalf("request %d should be allowed within the initial capacity", i)
		}
	}
	if tb.Allow("user1") {
		t.Fatal("6th immediate request should be blocked, bucket is empty")
	}
}

func TestTokenBucket_RefillsOverTime(t *testing.T) {
	tb := NewTokenBucket(2, 1) // capacity 2, refills 1 token per second
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tb.clock = func() time.Time { return fakeNow }

	tb.Allow("user1")
	tb.Allow("user1")
	if tb.Allow("user1") {
		t.Fatal("bucket should be empty after using both tokens")
	}

	fakeNow = fakeNow.Add(time.Second)
	if !tb.Allow("user1") {
		t.Fatal("one token should have refilled after one second")
	}
	if tb.Allow("user1") {
		t.Fatal("only one token should have refilled, this request should be blocked")
	}
}

func TestTokenBucket_ConcurrentAccessIsSafe(t *testing.T) {
	tb := NewTokenBucket(1000, 100)
	var wg sync.WaitGroup
	var allowed atomic.Int32

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tb.Allow("shared-key") {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 200 {
		t.Fatalf("expected all 200 concurrent requests within capacity to be allowed, got %d", allowed.Load())
	}
}
