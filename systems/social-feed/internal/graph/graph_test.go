package graph

import (
	"sort"
	"sync"
	"testing"
)

func TestGraph_FollowAndLookupBothDirections(t *testing.T) {
	g := New()
	g.Follow("alice", "bob")

	if got := g.Followers("bob"); len(got) != 1 || got[0] != "alice" {
		t.Fatalf("expected bob's followers to be [alice], got %v", got)
	}
	if got := g.Following("alice"); len(got) != 1 || got[0] != "bob" {
		t.Fatalf("expected alice to follow [bob], got %v", got)
	}
}

func TestGraph_FollowIsIdempotent(t *testing.T) {
	g := New()
	g.Follow("alice", "bob")
	g.Follow("alice", "bob")
	if g.FollowerCount("bob") != 1 {
		t.Fatalf("expected following twice to count once, got %d", g.FollowerCount("bob"))
	}
}

func TestGraph_CannotFollowYourself(t *testing.T) {
	g := New()
	g.Follow("alice", "alice")
	if g.FollowerCount("alice") != 0 {
		t.Fatal("expected a self-follow to be ignored")
	}
}

func TestGraph_Unfollow(t *testing.T) {
	g := New()
	g.Follow("alice", "bob")
	g.Unfollow("alice", "bob")
	if g.FollowerCount("bob") != 0 || len(g.Following("alice")) != 0 {
		t.Fatal("expected unfollow to remove the relationship in both directions")
	}
}

func TestGraph_FollowerCountMatchesFollowers(t *testing.T) {
	g := New()
	for _, u := range []string{"a", "b", "c"} {
		g.Follow(u, "star")
	}
	got := g.Followers("star")
	sort.Strings(got)
	if len(got) != 3 || g.FollowerCount("star") != 3 {
		t.Fatalf("expected 3 followers, got %v / count %d", got, g.FollowerCount("star"))
	}
}

func TestGraph_ConcurrentFollowsAreSafe(t *testing.T) {
	g := New()
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			g.Follow(string(rune('a'+i%26))+string(rune('a'+i/26)), "star")
		}(i)
	}
	wg.Wait()
	if g.FollowerCount("star") != 200 {
		t.Fatalf("expected 200 distinct followers after concurrent follows, got %d", g.FollowerCount("star"))
	}
}
