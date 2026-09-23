package cache

import (
	"sync"
	"testing"
	"time"
)

func TestCacheAside_HitAvoidsSourceCall(t *testing.T) {
	source := NewSlowStore(0)
	source.Seed("a", "1")
	c := NewCacheAsideStore(NewLRU(10, time.Minute), source)

	c.Get("a")                  // populates the cache
	source.Seed("a", "changed") // change upstream, cache should not notice
	v, ok := c.Get("a")

	if !ok || v != "1" {
		t.Fatalf("expected the cached value 1, got %q ok=%v", v, ok)
	}
	if got := source.Calls(); got != 1 {
		t.Fatalf("expected exactly 1 source call, the initial miss, got %d", got)
	}
}

func TestCacheAside_MissPopulatesCache(t *testing.T) {
	source := NewSlowStore(0)
	source.Seed("a", "1")
	c := NewCacheAsideStore(NewLRU(10, time.Minute), source)

	v, ok := c.Get("a")
	if !ok || v != "1" {
		t.Fatalf("expected a=1 on first read, got %q ok=%v", v, ok)
	}

	c.Get("a")
	if got := source.Calls(); got != 1 {
		t.Fatalf("expected only 1 source call across 2 reads, the second should hit the cache, got %d", got)
	}
}

func TestCacheAside_StampedeCallsSourceOnce(t *testing.T) {
	source := NewSlowStore(30 * time.Millisecond)
	source.Seed("hot-key", "value")
	c := NewCacheAsideStore(NewLRU(10, time.Minute), source)

	var wg sync.WaitGroup
	results := make([]string, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			v, _ := c.Get("hot-key")
			results[idx] = v
		}(i)
	}
	wg.Wait()

	if got := source.Calls(); got != 1 {
		t.Fatalf("expected 100 concurrent requests for a missing key to reach the source exactly once, reached it %d times", got)
	}
	for i, v := range results {
		if v != "value" {
			t.Fatalf("request %d got %q, expected all 100 to get the correct value", i, v)
		}
	}
}
