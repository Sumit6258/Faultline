package cache

import (
	"testing"
	"time"
)

func TestSlowStore_CountsCalls(t *testing.T) {
	s := NewSlowStore(0)
	s.Seed("a", "1")

	s.Get("a")
	s.Get("a")
	s.Get("b")

	if got := s.Calls(); got != 3 {
		t.Fatalf("expected 3 calls counted, got %d", got)
	}
}

func TestSlowStore_ReturnsSeededValue(t *testing.T) {
	s := NewSlowStore(0)
	s.Seed("a", "1")

	v, ok := s.Get("a")
	if !ok || v != "1" {
		t.Fatalf("expected a=1, got %q ok=%v", v, ok)
	}

	if _, ok := s.Get("missing"); ok {
		t.Fatal("expected a miss for an unseeded key")
	}
}

func TestSlowStore_RespectsLatency(t *testing.T) {
	s := NewSlowStore(20 * time.Millisecond)
	s.Seed("a", "1")

	start := time.Now()
	s.Get("a")
	elapsed := time.Since(start)

	if elapsed < 20*time.Millisecond {
		t.Fatalf("expected Get to take at least 20ms, took %s", elapsed)
	}
}
