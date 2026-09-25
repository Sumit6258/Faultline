package retry

import (
	"errors"
	"testing"
	"time"
)

func TestDo_SucceedsOnFirstAttempt(t *testing.T) {
	calls := 0
	err := Do(func() error {
		calls++
		return nil
	}, Fixed{Wait: time.Millisecond}, 3, func(time.Duration) { t.Fatal("should not sleep, the first attempt succeeded") })

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 call, got %d", calls)
	}
}

func TestDo_RetriesUntilSuccess(t *testing.T) {
	calls := 0
	var sleptFor []time.Duration
	err := Do(func() error {
		calls++
		if calls < 3 {
			return errors.New("not yet")
		}
		return nil
	}, Fixed{Wait: 10 * time.Millisecond}, 5, func(d time.Duration) {
		sleptFor = append(sleptFor, d)
	})

	if err != nil {
		t.Fatalf("expected eventual success, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected exactly 3 calls, 2 failures then a success, got %d", calls)
	}
	if len(sleptFor) != 2 {
		t.Fatalf("expected 2 sleeps between the 3 calls, got %d", len(sleptFor))
	}
}

func TestDo_ReturnsLastErrorAfterExhaustingAttempts(t *testing.T) {
	wantErr := errors.New("still failing")
	calls := 0
	err := Do(func() error {
		calls++
		return wantErr
	}, Fixed{Wait: time.Millisecond}, 3, func(time.Duration) {})

	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the last error to be returned, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected exactly maxAttempts calls, got %d", calls)
	}
}

func TestDo_DoesNotSleepAfterFinalAttempt(t *testing.T) {
	sleeps := 0
	Do(func() error {
		return errors.New("always fails")
	}, Fixed{Wait: time.Millisecond}, 3, func(time.Duration) {
		sleeps++
	})

	if sleeps != 2 {
		t.Fatalf("expected 2 sleeps for 3 attempts, none after the last, got %d", sleeps)
	}
}
