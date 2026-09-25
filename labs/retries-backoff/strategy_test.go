package retry

import (
	"testing"
	"time"
)

func TestFixed_AlwaysReturnsSameDelay(t *testing.T) {
	f := Fixed{Wait: 100 * time.Millisecond}
	for attempt := 0; attempt < 5; attempt++ {
		if got := f.Delay(attempt); got != 100*time.Millisecond {
			t.Fatalf("attempt %d: expected a constant 100ms, got %s", attempt, got)
		}
	}
}

func TestExponentialBackoff_DoublesEachAttempt(t *testing.T) {
	e := ExponentialBackoff{Base: 10 * time.Millisecond, Max: time.Hour}
	want := []time.Duration{10, 20, 40, 80}
	for attempt, w := range want {
		if got := e.Delay(attempt); got != w*time.Millisecond {
			t.Fatalf("attempt %d: expected %s, got %s", attempt, w*time.Millisecond, got)
		}
	}
}

func TestExponentialBackoff_CapsAtMax(t *testing.T) {
	e := ExponentialBackoff{Base: 10 * time.Millisecond, Max: 50 * time.Millisecond}
	if got := e.Delay(10); got != 50*time.Millisecond {
		t.Fatalf("expected a high attempt number to cap at 50ms, got %s", got)
	}
}

func TestExponentialBackoff_DoesNotOverflowOnLargeAttempt(t *testing.T) {
	e := ExponentialBackoff{Base: time.Second, Max: time.Minute}
	if got := e.Delay(100); got != time.Minute {
		t.Fatalf("expected an extreme attempt number to cap cleanly at Max, got %s", got)
	}
}

func TestFullJitter_StaysWithinCap(t *testing.T) {
	j := NewFullJitter(10*time.Millisecond, 1*time.Second, 1)
	cap := ExponentialBackoff{Base: 10 * time.Millisecond, Max: 1 * time.Second}
	for attempt := 0; attempt < 6; attempt++ {
		wantCap := cap.Delay(attempt)
		for i := 0; i < 200; i++ {
			got := j.Delay(attempt)
			if got < 0 || got >= wantCap {
				t.Fatalf("attempt %d: delay %s is outside [0, %s)", attempt, got, wantCap)
			}
		}
	}
}

func TestFullJitter_ProducesDifferentValues(t *testing.T) {
	j := NewFullJitter(100*time.Millisecond, time.Second, 1)
	seen := make(map[time.Duration]bool)
	for i := 0; i < 50; i++ {
		seen[j.Delay(0)] = true
	}
	if len(seen) < 40 {
		t.Fatalf("expected 50 draws from FullJitter to mostly differ from each other, only got %d distinct values", len(seen))
	}
}

func TestFullJitter_SameSeedIsReproducible(t *testing.T) {
	a := NewFullJitter(50*time.Millisecond, time.Second, 42)
	b := NewFullJitter(50*time.Millisecond, time.Second, 42)
	for i := 0; i < 10; i++ {
		if a.Delay(0) != b.Delay(0) {
			t.Fatal("expected two FullJitter strategies with the same seed to produce the same sequence")
		}
	}
}
