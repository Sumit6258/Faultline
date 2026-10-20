package timeline

import (
	"testing"
	"time"

	"github.com/Sumit6258/Faultline/systems/social-feed/internal/post"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func mkPost(seq int64, secs int) *post.Post {
	return &post.Post{ID: string(rune('a' + seq)), Seq: seq, CreatedAt: t0.Add(time.Duration(secs) * time.Second)}
}

func seqs(ps []*post.Post) []int64 {
	out := make([]int64, len(ps))
	for i, p := range ps {
		out[i] = p.Seq
	}
	return out
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMerge_InterleavesNewestFirst(t *testing.T) {
	a := []*post.Post{mkPost(5, 50), mkPost(3, 30), mkPost(1, 10)}
	b := []*post.Post{mkPost(4, 40), mkPost(2, 20)}
	got := seqs(mergeNewestFirst([][]*post.Post{a, b}, 10))
	if want := []int64{5, 4, 3, 2, 1}; !equal(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestMerge_RespectsLimit(t *testing.T) {
	a := []*post.Post{mkPost(3, 30), mkPost(1, 10)}
	b := []*post.Post{mkPost(2, 20)}
	got := seqs(mergeNewestFirst([][]*post.Post{a, b}, 2))
	if want := []int64{3, 2}; !equal(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestMerge_EmitsAPostThatAppearsInTwoListsOnce(t *testing.T) {
	shared := mkPost(2, 20)
	a := []*post.Post{mkPost(3, 30), shared, mkPost(1, 10)}
	b := []*post.Post{shared}
	got := seqs(mergeNewestFirst([][]*post.Post{a, b}, 10))
	if want := []int64{3, 2, 1}; !equal(got, want) {
		t.Fatalf("expected the shared post once, %v, got %v", want, got)
	}
}

func TestMerge_BreaksTimestampTiesBySeq(t *testing.T) {
	a := []*post.Post{mkPost(1, 10)}
	b := []*post.Post{mkPost(2, 10)}
	got := seqs(mergeNewestFirst([][]*post.Post{a, b}, 10))
	if want := []int64{2, 1}; !equal(got, want) {
		t.Fatalf("expected the later seq first on a tie, %v, got %v", want, got)
	}
}

func TestMerge_HandlesEmptyInputs(t *testing.T) {
	if got := mergeNewestFirst(nil, 5); len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
	if got := mergeNewestFirst([][]*post.Post{{}, {}}, 5); len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}
