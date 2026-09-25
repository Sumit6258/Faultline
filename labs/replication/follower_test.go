package replication

import (
	"testing"
	"time"
)

func newTestFollower(leader *Leader, lag time.Duration, fakeNow *time.Time) *Follower {
	f := NewFollower(leader, lag)
	f.clock = func() time.Time { return *fakeNow }
	return f
}

func TestFollower_ReadIsStaleBeforeLagElapses(t *testing.T) {
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	leader := NewLeader()
	leader.clock = func() time.Time { return fakeNow }
	follower := newTestFollower(leader, 100*time.Millisecond, &fakeNow)

	leader.Write("session:abc", "logged-in")
	follower.Sync() // called immediately, same instant as the write

	if _, ok := follower.Read("session:abc"); ok {
		t.Fatal("expected the write to not be visible yet, lag has not elapsed")
	}
}

func TestFollower_ReadIsFreshAfterLagElapses(t *testing.T) {
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	leader := NewLeader()
	leader.clock = func() time.Time { return fakeNow }
	follower := newTestFollower(leader, 100*time.Millisecond, &fakeNow)

	leader.Write("session:abc", "logged-in")
	fakeNow = fakeNow.Add(150 * time.Millisecond)
	follower.Sync()

	v, ok := follower.Read("session:abc")
	if !ok || v != "logged-in" {
		t.Fatalf("expected the write to be visible once lag has elapsed, got %q ok=%v", v, ok)
	}
}

func TestFollower_SyncStopsAtFirstTooRecentEntry(t *testing.T) {
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	leader := NewLeader()
	leader.clock = func() time.Time { return fakeNow }
	follower := newTestFollower(leader, 100*time.Millisecond, &fakeNow)

	leader.Write("a", "1")
	fakeNow = fakeNow.Add(150 * time.Millisecond) // "a" is now old enough
	leader.Write("b", "2")                        // "b" is written right now, too recent

	applied := follower.Sync()
	if applied != 1 {
		t.Fatalf("expected exactly 1 entry to be old enough to apply, got %d", applied)
	}
	if _, ok := follower.Read("a"); !ok {
		t.Fatal("expected a to be applied, it's old enough")
	}
	if _, ok := follower.Read("b"); ok {
		t.Fatal("expected b to not be applied yet, it was just written")
	}
}

func TestFollower_SyncIsIdempotentWithNoNewEntries(t *testing.T) {
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	leader := NewLeader()
	leader.clock = func() time.Time { return fakeNow }
	follower := newTestFollower(leader, 0, &fakeNow)

	leader.Write("a", "1")
	first := follower.Sync()
	second := follower.Sync()

	if first != 1 {
		t.Fatalf("expected the first sync to apply 1 entry, got %d", first)
	}
	if second != 0 {
		t.Fatalf("expected the second sync, with nothing new, to apply 0 entries, got %d", second)
	}
}

func TestFollower_LagBehindReportsUnappliedCount(t *testing.T) {
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	leader := NewLeader()
	leader.clock = func() time.Time { return fakeNow }
	follower := newTestFollower(leader, 100*time.Millisecond, &fakeNow)

	leader.Write("a", "1")
	leader.Write("b", "2")
	leader.Write("c", "3")

	if got := follower.LagBehind(); got != 3 {
		t.Fatalf("expected the follower to be 3 entries behind before any sync, got %d", got)
	}

	fakeNow = fakeNow.Add(150 * time.Millisecond)
	follower.Sync()

	if got := follower.LagBehind(); got != 0 {
		t.Fatalf("expected the follower to be caught up after syncing past the lag, got %d", got)
	}
}

func TestFollower_ZeroLagAppliesImmediately(t *testing.T) {
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	leader := NewLeader()
	leader.clock = func() time.Time { return fakeNow }
	follower := newTestFollower(leader, 0, &fakeNow)

	leader.Write("a", "1")
	follower.Sync()

	if _, ok := follower.Read("a"); !ok {
		t.Fatal("expected a zero lag follower to see a write immediately")
	}
}
