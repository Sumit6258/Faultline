package post

import (
	"testing"
	"time"
)

func TestStore_CreateAndGet(t *testing.T) {
	s := NewStore()
	p := s.Create("alice", "hello")
	got, ok := s.Get(p.ID)
	if !ok || got.Body != "hello" || got.Author != "alice" {
		t.Fatalf("expected to get the created post back, got %v ok=%v", got, ok)
	}
}

func TestStore_IDsAreUnique(t *testing.T) {
	s := NewStore()
	a, b := s.Create("alice", "1"), s.Create("alice", "2")
	if a.ID == b.ID {
		t.Fatal("expected distinct ids")
	}
}

func TestStore_ByAuthorIsNewestFirst(t *testing.T) {
	s := NewStore()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { now = now.Add(time.Second); return now })

	s.Create("alice", "first")
	s.Create("alice", "second")
	s.Create("alice", "third")

	got := s.ByAuthor("alice", 10)
	if len(got) != 3 || got[0].Body != "third" || got[2].Body != "first" {
		t.Fatalf("expected newest first, got %v", got)
	}
}

func TestStore_ByAuthorRespectsLimit(t *testing.T) {
	s := NewStore()
	for i := 0; i < 5; i++ {
		s.Create("alice", "x")
	}
	if got := s.ByAuthor("alice", 2); len(got) != 2 {
		t.Fatalf("expected limit of 2 respected, got %d", len(got))
	}
}

func TestStore_ByAuthorOnlyReturnsThatAuthor(t *testing.T) {
	s := NewStore()
	s.Create("alice", "a")
	s.Create("bob", "b")
	got := s.ByAuthor("alice", 10)
	if len(got) != 1 || got[0].Author != "alice" {
		t.Fatalf("expected only alice's post, got %v", got)
	}
}

func TestStore_SeqIncreasesWithCreationOrder(t *testing.T) {
	s := NewStore()
	a, b := s.Create("alice", "1"), s.Create("bob", "2")
	if b.Seq <= a.Seq {
		t.Fatalf("expected Seq to increase, got %d then %d", a.Seq, b.Seq)
	}
}
