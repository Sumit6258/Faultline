package ratelimit

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFixedWindow_AllowsUpToLimitThenBlocks(t *testing.T) {
	fw := NewFixedWindow(3, time.Second)
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fw.clock = func() time.Time { return fakeNow }

	for i := 0; i < 3; i++ {
		if !fw.Allow("user1") {
			t.Fatalf("request %d should be allowed", i)
		}
	}
	if fw.Allow("user1") {
		t.Fatal("4th request in the same window should be blocked")
	}
}

func TestFixedWindow_ResetsOnNewWindow(t *testing.T) {
	fw := NewFixedWindow(2, time.Second)
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fw.clock = func() time.Time { return fakeNow }

	fw.Allow("user1")
	fw.Allow("user1")
	if fw.Allow("user1") {
		t.Fatal("3rd request in the same window should be blocked")
	}

	fakeNow = fakeNow.Add(time.Second)
	if !fw.Allow("user1") {
		t.Fatal("request in a new window should be allowed")
	}
}

func TestFixedWindow_KeysAreIndependent(t *testing.T) {
	fw := NewFixedWindow(1, time.Second)
	if !fw.Allow("a") {
		t.Fatal("first request for key a should be allowed")
	}
	if !fw.Allow("b") {
		t.Fatal("first request for key b should be allowed, independent of key a")
	}
}

func TestFixedWindow_ConcurrentAccessIsSafe(t *testing.T) {
	fw := NewFixedWindow(1000, time.Minute)
	var wg sync.WaitGroup
	var allowed atomic.Int32

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if fw.Allow("shared-key") {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 200 {
		t.Fatalf("expected all 200 concurrent requests within the limit to be allowed, got %d", allowed.Load())
	}
}
