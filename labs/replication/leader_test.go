package replication

import "testing"

func TestLeader_WriteAssignsIncreasingSequence(t *testing.T) {
	l := NewLeader()
	e1 := l.Write("a", "1")
	e2 := l.Write("b", "2")
	if e1.Sequence != 1 || e2.Sequence != 2 {
		t.Fatalf("expected sequences 1 then 2, got %d then %d", e1.Sequence, e2.Sequence)
	}
}

func TestLeader_EntriesSinceReturnsOnlyNewer(t *testing.T) {
	l := NewLeader()
	l.Write("a", "1")
	l.Write("b", "2")
	l.Write("c", "3")

	entries := l.EntriesSince(1)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries after sequence 1, got %d", len(entries))
	}
	if entries[0].Key != "b" || entries[1].Key != "c" {
		t.Fatalf("expected entries b then c, got %s then %s", entries[0].Key, entries[1].Key)
	}
}

func TestLeader_LatestSequence(t *testing.T) {
	l := NewLeader()
	if l.LatestSequence() != 0 {
		t.Fatal("expected sequence 0 on a fresh leader")
	}
	l.Write("a", "1")
	l.Write("b", "2")
	if l.LatestSequence() != 2 {
		t.Fatalf("expected latest sequence 2, got %d", l.LatestSequence())
	}
}
