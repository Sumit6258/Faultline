package dedup

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestTracker_FirstClaimSucceeds(t *testing.T) {
	tr := NewTracker()
	if !tr.Claim("a") {
		t.Fatal("expected the first claim on a new id to succeed")
	}
}

func TestTracker_SecondClaimOnSameIDFails(t *testing.T) {
	tr := NewTracker()
	tr.Claim("a")
	if tr.Claim("a") {
		t.Fatal("expected a second claim on the same id to fail")
	}
}

func TestTracker_DifferentIDsAreIndependent(t *testing.T) {
	tr := NewTracker()
	if !tr.Claim("a") || !tr.Claim("b") {
		t.Fatal("expected distinct ids to each claim successfully")
	}
}

func TestTracker_ConcurrentClaimsOnlyOneWins(t *testing.T) {
	tr := NewTracker()
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tr.Claim("shared-id") {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := wins.Load(); got != 1 {
		t.Fatalf("expected exactly 1 of 50 concurrent claims on the same id to win, got %d", got)
	}
}
