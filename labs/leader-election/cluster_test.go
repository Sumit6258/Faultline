package leaderelection

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCluster_FirstHeartbeatBecomesLeader(t *testing.T) {
	c := NewCluster(time.Second)
	term, isLeader := c.Heartbeat("node-1")
	if !isLeader || term != 1 {
		t.Fatalf("expected the first heartbeat to win leadership at term 1, got term=%d isLeader=%v", term, isLeader)
	}
}

func TestCluster_OtherNodeRefusedWhileLeaderAlive(t *testing.T) {
	c := NewCluster(time.Second)
	c.Heartbeat("node-1")

	_, isLeader := c.Heartbeat("node-2")
	if isLeader {
		t.Fatal("node-2 should not become leader while node-1's heartbeat is still fresh")
	}
}

func TestCluster_LeaderReaffirmsWithoutChangingTerm(t *testing.T) {
	c := NewCluster(time.Second)
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.clock = func() time.Time { return fakeNow }

	firstTerm, _ := c.Heartbeat("node-1")
	fakeNow = fakeNow.Add(200 * time.Millisecond)
	secondTerm, isLeader := c.Heartbeat("node-1")

	if !isLeader {
		t.Fatal("node-1 should remain leader on a timely heartbeat")
	}
	if secondTerm != firstTerm {
		t.Fatalf("expected the term to stay at %d on a normal reaffirming heartbeat, got %d", firstTerm, secondTerm)
	}
}

func TestCluster_NewLeaderElectedAfterTimeout(t *testing.T) {
	c := NewCluster(time.Second)
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.clock = func() time.Time { return fakeNow }

	firstTerm, _ := c.Heartbeat("node-1")

	// node-1 goes silent for longer than the timeout.
	fakeNow = fakeNow.Add(2 * time.Second)

	secondTerm, isLeader := c.Heartbeat("node-2")
	if !isLeader {
		t.Fatal("node-2 should win leadership once node-1 has gone silent past the timeout")
	}
	if secondTerm <= firstTerm {
		t.Fatalf("expected the term to increase after a leadership change, went from %d to %d", firstTerm, secondTerm)
	}
}

func TestCluster_SameLeaderLateHeartbeatStillBumpsTerm(t *testing.T) {
	// If the leader's own heartbeat arrives late, past the timeout, the
	// cluster can't tell whether someone else might have taken over in
	// that gap. Bumping the term even though the leader name is unchanged
	// is the conservative, safe choice.
	c := NewCluster(time.Second)
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.clock = func() time.Time { return fakeNow }

	firstTerm, _ := c.Heartbeat("node-1")
	fakeNow = fakeNow.Add(2 * time.Second)
	secondTerm, isLeader := c.Heartbeat("node-1")

	if !isLeader {
		t.Fatal("node-1 should be able to reclaim leadership, nobody else took over")
	}
	if secondTerm <= firstTerm {
		t.Fatalf("expected a late self heartbeat to still bump the term, went from %d to %d", firstTerm, secondTerm)
	}
}

func TestCluster_TermNeverDecreases(t *testing.T) {
	c := NewCluster(100 * time.Millisecond)
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.clock = func() time.Time { return fakeNow }

	var lastTerm int64
	nodes := []string{"node-1", "node-2", "node-3", "node-1", "node-2"}
	for _, n := range nodes {
		term, _ := c.Heartbeat(n)
		if term < lastTerm {
			t.Fatalf("term decreased from %d to %d", lastTerm, term)
		}
		lastTerm = term
		fakeNow = fakeNow.Add(200 * time.Millisecond) // past the timeout each round
	}
}

func TestCluster_ConcurrentHeartbeatsOnlyOneBecomesLeader(t *testing.T) {
	c := NewCluster(time.Minute)
	var wg sync.WaitGroup
	var wins atomic.Int32

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_, isLeader := c.Heartbeat(fmt.Sprintf("node-%d", id))
			if isLeader {
				wins.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if got := wins.Load(); got != 1 {
		t.Fatalf("expected exactly 1 of 50 concurrent heartbeats to win leadership, got %d", got)
	}
}
