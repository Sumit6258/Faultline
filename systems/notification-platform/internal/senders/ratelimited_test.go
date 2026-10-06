package senders

import (
	"testing"
	"time"
)

func TestRateLimited_AllowsUpToCapacityThenBlocks(t *testing.T) {
	inner := NewLocalSender("sms", AlwaysSucceed)
	rl := NewRateLimited(inner, 3, 1)
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rl.clock = func() time.Time { return fakeNow }

	for i := 0; i < 3; i++ {
		if err := rl.Send("x", "y"); err != nil {
			t.Fatalf("send %d should be within capacity, got %v", i, err)
		}
	}
	if err := rl.Send("x", "y"); err != ErrRateLimited {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}
	if len(inner.Sent()) != 3 {
		t.Fatalf("expected exactly 3 to actually reach the provider, got %d", len(inner.Sent()))
	}
}

func TestRateLimited_RefillsOverTime(t *testing.T) {
	inner := NewLocalSender("sms", AlwaysSucceed)
	rl := NewRateLimited(inner, 1, 1)
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rl.clock = func() time.Time { return fakeNow }

	rl.Send("x", "y")
	if err := rl.Send("x", "y"); err != ErrRateLimited {
		t.Fatal("expected the second immediate send to be rate limited")
	}
	fakeNow = fakeNow.Add(time.Second)
	if err := rl.Send("x", "y"); err != nil {
		t.Fatalf("expected a send to succeed after refilling for 1 second, got %v", err)
	}
}
