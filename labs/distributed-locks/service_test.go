package distlock

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestService_FirstAcquireSucceeds(t *testing.T) {
	s := NewService()
	token, ok := s.TryAcquire("resource-1", "worker-A", time.Minute)
	if !ok || token != 1 {
		t.Fatalf("expected the first acquire to succeed with token 1, got token=%d ok=%v", token, ok)
	}
}

func TestService_SecondHolderBlockedWhileLeaseValid(t *testing.T) {
	s := NewService()
	s.TryAcquire("resource-1", "worker-A", time.Minute)

	_, ok := s.TryAcquire("resource-1", "worker-B", time.Minute)
	if ok {
		t.Fatal("worker-B should not acquire the lease, worker-A's is still valid")
	}
}

func TestService_SecondHolderCanAcquireAfterExpiry(t *testing.T) {
	s := NewService()
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return fakeNow }

	s.TryAcquire("resource-1", "worker-A", time.Second)
	fakeNow = fakeNow.Add(2 * time.Second)

	token, ok := s.TryAcquire("resource-1", "worker-B", time.Minute)
	if !ok {
		t.Fatal("worker-B should acquire the lease once worker-A's has expired")
	}
	if token != 2 {
		t.Fatalf("expected worker-B's token to be 2, got %d", token)
	}
}

func TestService_RenewExtendsLease(t *testing.T) {
	s := NewService()
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return fakeNow }

	token, _ := s.TryAcquire("resource-1", "worker-A", time.Second)
	fakeNow = fakeNow.Add(500 * time.Millisecond)

	if !s.Renew("resource-1", "worker-A", token, time.Second) {
		t.Fatal("worker-A should be able to renew its own lease")
	}

	fakeNow = fakeNow.Add(700 * time.Millisecond) // 1.2s after the renew, still under the fresh 1s ttl
	if _, ok := s.TryAcquire("resource-1", "worker-B", time.Second); ok {
		t.Fatal("worker-B should still be blocked, worker-A's renewed lease has not expired")
	}
}

func TestService_RenewFailsWithWrongToken(t *testing.T) {
	s := NewService()
	s.TryAcquire("resource-1", "worker-A", time.Minute)

	if s.Renew("resource-1", "worker-A", 999, time.Minute) {
		t.Fatal("renew with the wrong token should fail")
	}
}

func TestService_ReleaseAllowsImmediateReacquire(t *testing.T) {
	s := NewService()
	token, _ := s.TryAcquire("resource-1", "worker-A", time.Minute)

	if !s.Release("resource-1", "worker-A", token) {
		t.Fatal("worker-A should be able to release its own lease")
	}

	if _, ok := s.TryAcquire("resource-1", "worker-B", time.Minute); !ok {
		t.Fatal("worker-B should be able to acquire immediately after release")
	}
}

func TestService_TokensAreMonotonicallyIncreasing(t *testing.T) {
	s := NewService()
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.clock = func() time.Time { return fakeNow }

	var lastToken int64
	for i := 0; i < 5; i++ {
		token, _ := s.TryAcquire("resource-1", fmt.Sprintf("worker-%d", i), time.Millisecond)
		if token <= lastToken {
			t.Fatalf("expected each new token to be higher than the last, got %d after %d", token, lastToken)
		}
		lastToken = token
		fakeNow = fakeNow.Add(2 * time.Millisecond) // let the previous lease expire
	}
}

func TestService_ConcurrentAcquireOnlyOneWins(t *testing.T) {
	s := NewService()
	var wg sync.WaitGroup
	var successes atomic.Int32

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if _, ok := s.TryAcquire("shared-resource", fmt.Sprintf("worker-%d", id), time.Minute); ok {
				successes.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("expected exactly 1 of 50 concurrent acquire attempts to succeed, got %d", got)
	}
}
