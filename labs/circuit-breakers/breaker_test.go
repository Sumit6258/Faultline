package circuitbreaker

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errDependencyDown = errors.New("dependency down")

func TestBreaker_StaysClosedOnSuccess(t *testing.T) {
	b := NewBreaker(3, time.Second)
	for i := 0; i < 5; i++ {
		if err := b.Call(func() error { return nil }); err != nil {
			t.Fatalf("call %d: expected no error, got %v", i, err)
		}
	}
	if b.State() != Closed {
		t.Fatalf("expected the breaker to stay closed on repeated success, got %s", b.State())
	}
}

func TestBreaker_OpensAfterThresholdConsecutiveFailures(t *testing.T) {
	b := NewBreaker(3, time.Second)
	failing := func() error { return errDependencyDown }

	for i := 0; i < 3; i++ {
		b.Call(failing)
	}

	if b.State() != Open {
		t.Fatalf("expected the breaker to open after 3 consecutive failures, got %s", b.State())
	}
}

func TestBreaker_OpenRejectsWithoutCallingFn(t *testing.T) {
	b := NewBreaker(1, time.Minute)
	b.Call(func() error { return errDependencyDown }) // opens it

	called := false
	err := b.Call(func() error {
		called = true
		return nil
	})

	if err != ErrCircuitOpen {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
	if called {
		t.Fatal("the wrapped function should not have been called while the circuit is open")
	}
}

func TestBreaker_StaysOpenBeforeCooldownElapses(t *testing.T) {
	b := NewBreaker(1, time.Second)
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b.clock = func() time.Time { return fakeNow }

	b.Call(func() error { return errDependencyDown })
	fakeNow = fakeNow.Add(500 * time.Millisecond) // half the cooldown

	err := b.Call(func() error { return nil })
	if err != ErrCircuitOpen {
		t.Fatalf("expected the circuit to still be open before the cooldown elapses, got err=%v state=%s", err, b.State())
	}
}

func TestBreaker_HalfOpenSuccessCloses(t *testing.T) {
	b := NewBreaker(1, time.Second)
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b.clock = func() time.Time { return fakeNow }

	b.Call(func() error { return errDependencyDown })
	fakeNow = fakeNow.Add(2 * time.Second) // past the cooldown

	err := b.Call(func() error { return nil }) // the half-open trial succeeds
	if err != nil {
		t.Fatalf("expected the half-open trial to succeed, got %v", err)
	}
	if b.State() != Closed {
		t.Fatalf("expected a successful half-open trial to close the circuit, got %s", b.State())
	}
}

func TestBreaker_HalfOpenFailureReopens(t *testing.T) {
	b := NewBreaker(1, time.Second)
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b.clock = func() time.Time { return fakeNow }

	b.Call(func() error { return errDependencyDown })
	fakeNow = fakeNow.Add(2 * time.Second)

	b.Call(func() error { return errDependencyDown }) // the half-open trial also fails

	if b.State() != Open {
		t.Fatalf("expected a failed half-open trial to reopen the circuit, got %s", b.State())
	}
}

func TestBreaker_SuccessResetsConsecutiveFailureCount(t *testing.T) {
	b := NewBreaker(3, time.Second)
	b.Call(func() error { return errDependencyDown })
	b.Call(func() error { return errDependencyDown })
	b.Call(func() error { return nil }) // resets the streak before it hits the threshold
	b.Call(func() error { return errDependencyDown })
	b.Call(func() error { return errDependencyDown })

	if b.State() != Closed {
		t.Fatalf("expected the breaker to stay closed, no 3 consecutive failures happened, got %s", b.State())
	}
}

func TestBreaker_ConcurrentCallsOnlyOneTrialInHalfOpen(t *testing.T) {
	b := NewBreaker(1, time.Second)
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	b.clock = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return fakeNow
	}

	b.Call(func() error { return errDependencyDown }) // opens it
	clockMu.Lock()
	fakeNow = fakeNow.Add(2 * time.Second)
	clockMu.Unlock()

	var wg sync.WaitGroup
	var trialsRun atomic.Int32
	var rejections atomic.Int32
	release := make(chan struct{})

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := b.Call(func() error {
				trialsRun.Add(1)
				<-release // hold the trial open so the others are forced to race against it
				return nil
			})
			if err == ErrCircuitOpen {
				rejections.Add(1)
			}
		}()
	}

	time.Sleep(50 * time.Millisecond) // let all 10 goroutines reach Call
	close(release)
	wg.Wait()

	if got := trialsRun.Load(); got != 1 {
		t.Fatalf("expected exactly 1 half-open trial to actually run concurrently, got %d", got)
	}
	if got := rejections.Load(); got != 9 {
		t.Fatalf("expected the other 9 concurrent calls to be rejected while the trial was in flight, got %d", got)
	}
}
