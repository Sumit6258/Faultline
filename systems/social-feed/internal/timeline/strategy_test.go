package timeline

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/Sumit6258/Faultline/systems/social-feed/internal/graph"
	"github.com/Sumit6258/Faultline/systems/social-feed/internal/post"
)

type world struct {
	g     *graph.Graph
	posts *post.Store
	now   time.Time
}

func newWorld() *world {
	w := &world{g: graph.New(), posts: post.NewStore(), now: t0}
	w.posts.SetClock(func() time.Time {
		w.now = w.now.Add(time.Second)
		return w.now
	})
	return w
}

// publish creates one post and hands it to every strategy given, the same
// order of events a real service would produce.
func (w *world) publish(author string, strategies ...Strategy) *post.Post {
	p := w.posts.Create(author, "body")
	for _, s := range strategies {
		s.OnPost(p)
	}
	return p
}

func ids(ps []*post.Post) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}

func sameStrings(a, b []string) bool {
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

func TestFanoutOnWrite_PostReachesEveryFollowerInbox(t *testing.T) {
	w := newWorld()
	f := NewFanoutOnWrite(w.g, 100)
	w.g.Follow("alice", "star")
	w.g.Follow("bob", "star")

	p := w.publish("star", f)

	for _, u := range []string{"alice", "bob"} {
		if got := ids(f.GetTimeline(u, 10)); !sameStrings(got, []string{p.ID}) {
			t.Fatalf("expected %s's inbox to hold the post, got %v", u, got)
		}
	}
}

func TestFanoutOnWrite_NonFollowersAndTheAuthorGetNothing(t *testing.T) {
	w := newWorld()
	f := NewFanoutOnWrite(w.g, 100)
	w.g.Follow("alice", "star")

	w.publish("star", f)

	for _, u := range []string{"star", "stranger"} {
		if got := f.GetTimeline(u, 10); len(got) != 0 {
			t.Fatalf("expected %s to see nothing, got %v", u, ids(got))
		}
	}
}

func TestFanoutOnWrite_OnlyPostsMadeAfterFollowingAppear(t *testing.T) {
	// A real, known property of precomputed inboxes: they only contain
	// what was pushed into them. Following someone does not backfill their
	// earlier posts. Compare TestFanoutOnRead_FollowingLaterStillShowsRecentPosts.
	w := newWorld()
	f := NewFanoutOnWrite(w.g, 100)

	w.publish("star", f)
	w.g.Follow("alice", "star")
	after := w.publish("star", f)

	if got := ids(f.GetTimeline("alice", 10)); !sameStrings(got, []string{after.ID}) {
		t.Fatalf("expected only the post made after following, got %v", got)
	}
}

func TestFanoutOnRead_TimelineMergesEveryFolloweeNewestFirst(t *testing.T) {
	w := newWorld()
	f := NewFanoutOnRead(w.g, w.posts)
	w.g.Follow("alice", "bob")
	w.g.Follow("alice", "carol")

	p1 := w.publish("bob", f)
	p2 := w.publish("carol", f)
	p3 := w.publish("bob", f)
	w.publish("dave", f) // alice doesn't follow dave

	if got, want := ids(f.GetTimeline("alice", 10)), []string{p3.ID, p2.ID, p1.ID}; !sameStrings(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestFanoutOnRead_FollowingLaterStillShowsRecentPosts(t *testing.T) {
	w := newWorld()
	f := NewFanoutOnRead(w.g, w.posts)

	before := w.publish("star", f)
	w.g.Follow("alice", "star")

	if got := ids(f.GetTimeline("alice", 10)); !sameStrings(got, []string{before.ID}) {
		t.Fatalf("expected a post made before following to appear, got %v", got)
	}
}

func TestFanoutOnRead_RespectsLimit(t *testing.T) {
	w := newWorld()
	f := NewFanoutOnRead(w.g, w.posts)
	w.g.Follow("alice", "bob")
	for i := 0; i < 10; i++ {
		w.publish("bob", f)
	}
	if got := f.GetTimeline("alice", 3); len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
}

func TestHybrid_OrdinaryAuthorIsFannedOutIntoInboxes(t *testing.T) {
	w := newWorld()
	h := NewHybrid(w.g, w.posts, 100, 5)
	w.g.Follow("alice", "bob")

	p := w.publish("bob", h)

	if got := ids(h.inboxes.read("alice", 10)); !sameStrings(got, []string{p.ID}) {
		t.Fatalf("expected the post to be pushed into alice's inbox, got %v", got)
	}
}

func TestHybrid_CelebrityIsNotFannedOutButStillReachesTheTimeline(t *testing.T) {
	w := newWorld()
	h := NewHybrid(w.g, w.posts, 100, 3)
	for _, u := range []string{"a", "b", "c"} {
		w.g.Follow(u, "star")
	}

	p := w.publish("star", h)

	for _, u := range []string{"a", "b", "c"} {
		if got := h.inboxes.read(u, 10); len(got) != 0 {
			t.Fatalf("expected nothing pushed into %s's inbox for a celebrity post, got %v", u, ids(got))
		}
		if got := ids(h.GetTimeline(u, 10)); !sameStrings(got, []string{p.ID}) {
			t.Fatalf("expected %s to still see the celebrity post at read time, got %v", u, got)
		}
	}
}

func TestHybrid_MergesInboxAndCelebrityPostsInOrder(t *testing.T) {
	w := newWorld()
	h := NewHybrid(w.g, w.posts, 100, 3)
	for _, u := range []string{"reader", "x", "y"} {
		w.g.Follow(u, "star")
	}
	w.g.Follow("reader", "friend")

	p1 := w.publish("friend", h) // ordinary, goes through the inbox
	p2 := w.publish("star", h)   // celebrity, merged at read time
	p3 := w.publish("friend", h)
	p4 := w.publish("star", h)

	if got, want := ids(h.GetTimeline("reader", 10)), []string{p4.ID, p3.ID, p2.ID, p1.ID}; !sameStrings(got, want) {
		t.Fatalf("expected the two sources interleaved newest first, %v, got %v", want, got)
	}
}

func TestHybrid_CelebrityStatusIsSticky(t *testing.T) {
	w := newWorld()
	h := NewHybrid(w.g, w.posts, 100, 3)
	for _, u := range []string{"keeper", "leaver1", "leaver2"} {
		w.g.Follow(u, "star")
	}

	p1 := w.publish("star", h) // above threshold: never pushed anywhere

	w.g.Unfollow("leaver1", "star")
	w.g.Unfollow("leaver2", "star") // now below the threshold

	p2 := w.publish("star", h)

	// If celebrity status were recomputed from the current follower count,
	// p1 would vanish from keeper's timeline: it was never in an inbox and
	// star would no longer be merged at read time.
	if got, want := ids(h.GetTimeline("keeper", 10)), []string{p2.ID, p1.ID}; !sameStrings(got, want) {
		t.Fatalf("expected both posts to survive a drop below the threshold, %v, got %v", want, got)
	}
}

func TestHybrid_CrossingTheThresholdUpwardShowsEachPostOnce(t *testing.T) {
	w := newWorld()
	h := NewHybrid(w.g, w.posts, 100, 3)
	w.g.Follow("r1", "x")
	w.g.Follow("r2", "x")

	p1 := w.publish("x", h) // 2 followers, below threshold: pushed into inboxes

	w.g.Follow("r3", "x") // 3 followers, x is now a celebrity
	p2 := w.publish("x", h)

	// r1's inbox holds p1, and the celebrity merge also returns p1 from the
	// post store. It must appear once.
	if got, want := ids(h.GetTimeline("r1", 10)), []string{p2.ID, p1.ID}; !sameStrings(got, want) {
		t.Fatalf("expected each post once, %v, got %v", want, got)
	}
}

// All three strategies decide where the work happens, not what the reader
// sees, so given the same graph and the same posts they must return the
// same timeline. This is the test that would catch any of them quietly
// dropping or reordering a post.
func TestStrategiesReturnTheSameTimeline(t *testing.T) {
	const users, followsEach, posts, limit, threshold = 40, 10, 300, 25, 8

	rng := rand.New(rand.NewSource(42))
	w := newWorld()
	name := func(i int) string { return fmt.Sprintf("u%d", i) }

	for u := 0; u < users; u++ {
		for _, f := range rng.Perm(users)[:followsEach] {
			w.g.Follow(name(u), name(f))
		}
	}
	celebrities := 0
	for u := 0; u < users; u++ {
		if w.g.FollowerCount(name(u)) >= threshold {
			celebrities++
		}
	}
	if celebrities < 5 || celebrities > users-5 {
		t.Fatalf("the comparison needs both celebrities and ordinary authors, got %d celebrities of %d", celebrities, users)
	}

	fow := NewFanoutOnWrite(w.g, 800)
	for_ := NewFanoutOnRead(w.g, w.posts)
	hy := NewHybrid(w.g, w.posts, 800, threshold)

	for i := 0; i < posts; i++ {
		w.publish(name(rng.Intn(users)), fow, for_, hy)
	}

	for u := 0; u < users; u++ {
		a := ids(fow.GetTimeline(name(u), limit))
		b := ids(for_.GetTimeline(name(u), limit))
		c := ids(hy.GetTimeline(name(u), limit))
		if !sameStrings(a, b) || !sameStrings(b, c) {
			t.Fatalf("%s: strategies disagree\n  fan-out on write: %v\n  fan-out on read:  %v\n  hybrid:           %v", name(u), a, b, c)
		}
	}
}

func TestStrategiesAreSafeUnderConcurrentPostsAndReads(t *testing.T) {
	for _, mk := range []struct {
		name string
		make func(w *world) Strategy
	}{
		{"fan-out on write", func(w *world) Strategy { return NewFanoutOnWrite(w.g, 200) }},
		{"fan-out on read", func(w *world) Strategy { return NewFanoutOnRead(w.g, w.posts) }},
		{"hybrid", func(w *world) Strategy { return NewHybrid(w.g, w.posts, 200, 10) }},
	} {
		t.Run(mk.name, func(t *testing.T) {
			w := newWorld()
			s := mk.make(w)
			for u := 0; u < 30; u++ {
				for f := 0; f < 30; f++ {
					w.g.Follow(fmt.Sprintf("u%d", u), fmt.Sprintf("u%d", f))
				}
			}
			var wg sync.WaitGroup
			for g := 0; g < 8; g++ {
				wg.Add(2)
				go func(g int) {
					defer wg.Done()
					for i := 0; i < 30; i++ {
						w.publish(fmt.Sprintf("u%d", (g*7+i)%30), s)
					}
				}(g)
				go func(g int) {
					defer wg.Done()
					for i := 0; i < 60; i++ {
						s.GetTimeline(fmt.Sprintf("u%d", (g+i)%30), 20)
					}
				}(g)
			}
			wg.Wait()
		})
	}
}

func TestInboxWritesCountsTheFanoutAmplification(t *testing.T) {
	w := newWorld()
	fow := NewFanoutOnWrite(w.g, 100)
	for_ := NewFanoutOnRead(w.g, w.posts)
	hy := NewHybrid(w.g, w.posts, 100, 5)

	for i := 0; i < 5; i++ {
		w.g.Follow(fmt.Sprintf("f%d", i), "star") // exactly the threshold, so star is a celebrity
	}
	for i := 0; i < 2; i++ {
		w.g.Follow(fmt.Sprintf("f%d", i), "pal") // 2 followers, ordinary
	}

	w.publish("star", fow, for_, hy)
	w.publish("pal", fow, for_, hy)

	if got := fow.InboxWrites(); got != 7 {
		t.Fatalf("fan-out on write: expected 5 + 2 = 7 inbox writes, got %d", got)
	}
	if got := for_.InboxWrites(); got != 0 {
		t.Fatalf("fan-out on read: expected 0 inbox writes, got %d", got)
	}
	if got := hy.InboxWrites(); got != 2 {
		t.Fatalf("hybrid: expected 2 (only the ordinary author fanned out), got %d", got)
	}
}

// A known limitation, pinned by a test so the README's claim about it is
// measured rather than reasoned. Unfollowing removes the edge from the
// graph, but a precomputed inbox still holds everything already pushed
// into it. Fan-out on read has no such state, so it forgets immediately.
func TestUnfollowTakesEffectImmediatelyOnlyUnderFanoutOnRead(t *testing.T) {
	w := newWorld()
	fow := NewFanoutOnWrite(w.g, 100)
	for_ := NewFanoutOnRead(w.g, w.posts)
	w.g.Follow("alice", "bob")
	p := w.publish("bob", fow, for_)

	w.g.Unfollow("alice", "bob")

	if got := for_.GetTimeline("alice", 10); len(got) != 0 {
		t.Fatalf("fan-out on read: expected the unfollowed author's post to vanish at once, got %v", ids(got))
	}
	if got := ids(fow.GetTimeline("alice", 10)); !sameStrings(got, []string{p.ID}) {
		t.Fatalf("fan-out on write: expected the already pushed post to remain in the inbox, got %v", got)
	}
}
