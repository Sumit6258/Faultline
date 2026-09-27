package ratelimit

import "testing"

func TestLimiter_AllowsUpToCapacity(t *testing.T) {
	l := NewLimiter(3, 1)
	for i := 0; i < 3; i++ {
		if !l.Allow("client-a") {
			t.Fatalf("request %d should be allowed within capacity", i)
		}
	}
	if l.Allow("client-a") {
		t.Fatal("4th immediate request should be blocked")
	}
}

func TestLimiter_ClientsAreIndependent(t *testing.T) {
	l := NewLimiter(1, 1)
	if !l.Allow("client-a") {
		t.Fatal("client-a's first request should be allowed")
	}
	if !l.Allow("client-b") {
		t.Fatal("client-b's first request should be allowed, independent of client-a")
	}
}
