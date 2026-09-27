package cache

import "testing"

func TestLRU_SetAndGet(t *testing.T) {
	c := NewLRU(10)
	c.Set("a", "1")
	v, ok := c.Get("a")
	if !ok || v != "1" {
		t.Fatalf("expected a=1, got %q ok=%v", v, ok)
	}
}

func TestLRU_EvictsLeastRecentlyUsed(t *testing.T) {
	c := NewLRU(2)
	c.Set("a", "1")
	c.Set("b", "2")
	c.Get("a") // touch a, b becomes the least recently used
	c.Set("c", "3")

	if _, ok := c.Get("b"); ok {
		t.Fatal("b should have been evicted")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a should still be present, it was touched more recently")
	}
}

func TestLRU_LenCapsAtCapacity(t *testing.T) {
	c := NewLRU(2)
	c.Set("a", "1")
	c.Set("b", "2")
	c.Set("c", "3")
	if c.Len() != 2 {
		t.Fatalf("expected length capped at 2, got %d", c.Len())
	}
}
