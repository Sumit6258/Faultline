package distlock

import "testing"

func TestFencedResource_FirstWriteAlwaysAccepted(t *testing.T) {
	r := NewFencedResource()
	if !r.Write(1, "hello") {
		t.Fatal("the first write to a fresh resource should always be accepted")
	}
}

func TestFencedResource_AcceptsHigherToken(t *testing.T) {
	r := NewFencedResource()
	r.Write(1, "first")
	if !r.Write(2, "second") {
		t.Fatal("a write with a higher token should be accepted")
	}
	if got := r.Value(); got != "second" {
		t.Fatalf("expected value %q, got %q", "second", got)
	}
}

func TestFencedResource_RejectsLowerToken(t *testing.T) {
	r := NewFencedResource()
	r.Write(5, "from token 5")
	if r.Write(3, "from a stale token 3") {
		t.Fatal("a write with a lower token than one already seen should be rejected")
	}
	if got := r.Value(); got != "from token 5" {
		t.Fatalf("expected the resource to still hold the value from token 5, got %q", got)
	}
}

func TestFencedResource_RejectsRepeatedStaleToken(t *testing.T) {
	r := NewFencedResource()
	r.Write(10, "current")
	for i := 0; i < 5; i++ {
		if r.Write(9, "stale attempt") {
			t.Fatal("a stale token should never succeed, no matter how many times it retries")
		}
	}
}
