package consistenthash

import (
	"strconv"
	"testing"
)

func TestNaiveModN_GetReturnsANode(t *testing.T) {
	n := NewNaiveModN("a", "b", "c")
	got := n.Get("some-key")
	if got != "a" && got != "b" && got != "c" {
		t.Fatalf("expected one of the configured nodes, got %q", got)
	}
}

func TestNaiveModN_SameKeyAlwaysMapsToSameNode(t *testing.T) {
	n := NewNaiveModN("a", "b", "c", "d")
	first := n.Get("stable-key")
	for i := 0; i < 50; i++ {
		if got := n.Get("stable-key"); got != first {
			t.Fatalf("expected a stable mapping while the node list is unchanged, got %q then %q", first, got)
		}
	}
}

func TestNaiveModN_AddingANodeRemapsMostKeys(t *testing.T) {
	// This is the property Ring exists to avoid. It's asserted here so the
	// naive implementation's weakness is provable, not just claimed in prose.
	before := NewNaiveModN("a", "b", "c", "d", "e")
	afterAdd := NewNaiveModN("a", "b", "c", "d", "e", "f")

	moved := 0
	const total = 5000
	for i := 0; i < total; i++ {
		key := "key-" + strconv.Itoa(i)
		if before.Get(key) != afterAdd.Get(key) {
			moved++
		}
	}

	if moved < total/2 {
		t.Fatalf("expected mod-N to remap the majority of keys when the node count changes, only %d of %d moved", moved, total)
	}
}
