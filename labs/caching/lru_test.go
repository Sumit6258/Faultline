package cache

import (
	"testing"
	"time"
)

func TestLRU_SetAndGet(t *testing.T) {
	c := NewLRU(10, time.Minute)
	c.Set("a", "1")
	v, ok := c.Get("a")
	if !ok || v != "1" {
		t.Fatalf("expected a=1, got %q ok=%v", v, ok)
	}
}

func TestLRU_MissingKey(t *testing.T) {
	c := NewLRU(10, time.Minute)
	if _, ok := c.Get("missing"); ok {
		t.Fatal("expected a miss for a key that was never set")
	}
}

func TestLRU_EvictsLeastRecentlyUsed(t *testing.T) {
	c := NewLRU(2, time.Minute)
	c.Set("a", "1")
	c.Set("b", "2")
	c.Get("a")      // touch a, so b becomes the least recently used
	c.Set("c", "3") // should evict b, not a

	if _, ok := c.Get("b"); ok {
		t.Fatal("b should have been evicted, it was the least recently used")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a should still be present, it was touched more recently than b")
	}
	if _, ok := c.Get("c"); !ok {
		t.Fatal("c should be present, it was just set")
	}
}

func TestLRU_TTLExpiry(t *testing.T) {
	c := NewLRU(10, time.Second)
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.clock = func() time.Time { return fakeNow }

	c.Set("a", "1")
	fakeNow = fakeNow.Add(2 * time.Second)

	if _, ok := c.Get("a"); ok {
		t.Fatal("entry should have expired after its TTL passed")
	}
}

func TestLRU_LenReflectsCapacity(t *testing.T) {
	c := NewLRU(3, time.Minute)
	c.Set("a", "1")
	c.Set("b", "2")
	c.Set("c", "3")
	c.Set("d", "4") // evicts one, capacity is 3
	if got := c.Len(); got != 3 {
		t.Fatalf("expected length capped at capacity 3, got %d", got)
	}
}
