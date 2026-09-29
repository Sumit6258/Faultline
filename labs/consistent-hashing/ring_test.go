package consistenthash

import (
	"hash/crc32"
	"strconv"
	"testing"
)

func TestRing_GetReturnsAddedNode(t *testing.T) {
	r := NewRing(10)
	r.AddNode("a")
	r.AddNode("b")

	got := r.Get("some-key")
	if got != "a" && got != "b" {
		t.Fatalf("expected Get to return one of the added nodes, got %q", got)
	}
}

func TestRing_EmptyRingReturnsEmptyString(t *testing.T) {
	r := NewRing(10)
	if got := r.Get("anything"); got != "" {
		t.Fatalf("expected empty string from an empty ring, got %q", got)
	}
}

func TestRing_SameKeyAlwaysMapsToSameNode(t *testing.T) {
	r := NewRing(50)
	for _, n := range []string{"a", "b", "c", "d"} {
		r.AddNode(n)
	}
	first := r.Get("stable-key")
	for i := 0; i < 100; i++ {
		if got := r.Get("stable-key"); got != first {
			t.Fatalf("expected the same key to always map to the same node while the ring is unchanged, got %q then %q", first, got)
		}
	}
}

func TestRing_RemovingNodeOnlyMovesItsOwnKeys(t *testing.T) {
	r := NewRing(100)
	nodes := []string{"a", "b", "c", "d", "e"}
	for _, n := range nodes {
		r.AddNode(n)
	}

	keys := make([]string, 2000)
	before := make(map[string]string, len(keys))
	for i := range keys {
		keys[i] = "key-" + strconv.Itoa(i)
		before[keys[i]] = r.Get(keys[i])
	}

	r.RemoveNode("c")

	for _, k := range keys {
		after := r.Get(k)
		if before[k] != "c" && after != before[k] {
			t.Fatalf("key %q mapped to %q before removal and moved to %q, but it wasn't on the removed node", k, before[k], after)
		}
	}
}

func TestRing_NodesIsSortedAndDeduplicated(t *testing.T) {
	r := NewRing(10)
	r.AddNode("b")
	r.AddNode("a")
	r.AddNode("a")
	got := r.Nodes()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("expected sorted, deduplicated [a b], got %v", got)
	}
}

// A 32 bit hash collides eventually, and with many virtual nodes per real
// node two different nodes can land on the exact same position. This test
// forces that by squeezing every hash into 16 slots, far fewer than the 24
// points being placed, so collisions between nodes are guaranteed.
func TestRing_HashCollisionsBetweenNodesDoNotCorruptTheRing(t *testing.T) {
	weak := func(s string) uint32 { return crc32.ChecksumIEEE([]byte(s)) % 16 }
	r := NewRing(8)
	r.hashFn = weak
	for _, n := range []string{"a", "b", "c"} {
		r.AddNode(n)
	}

	before := make(map[string]string)
	for i := 0; i < 500; i++ {
		k := "key-" + strconv.Itoa(i)
		before[k] = r.Get(k)
		if before[k] != "a" && before[k] != "b" && before[k] != "c" {
			t.Fatalf("%s mapped to %q with all three nodes present", k, before[k])
		}
	}

	r.RemoveNode("b")

	for i := 0; i < 500; i++ {
		k := "key-" + strconv.Itoa(i)
		after := r.Get(k)
		if after != "a" && after != "c" {
			t.Fatalf("%s mapped to %q after removing b, want a or c", k, after)
		}
		if before[k] != "b" && after != before[k] {
			t.Fatalf("%s moved from %s to %s although it was not on the removed node", k, before[k], after)
		}
	}
}
