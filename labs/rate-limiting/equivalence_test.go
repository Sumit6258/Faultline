package ratelimit

import (
	"math/rand"
	"testing"
	"time"
)

// LeakyBucket here is the meter reading of a leaky bucket: it rejects
// instead of queueing. Read that way it is the same mechanism as a token
// bucket viewed from the other side, tokens remaining equals capacity minus
// the water level, so the two accept exactly the same requests. This test
// exists so that fact is measured, not asserted in a README.
func TestLeakyBucketMeter_DecidesExactlyLikeTokenBucket(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tb := NewTokenBucket(5, 2)
	lb := NewLeakyBucket(5, 2)
	tb.clock = func() time.Time { return now }
	lb.clock = func() time.Time { return now }

	rng := rand.New(rand.NewSource(7))
	accepted, rejected := 0, 0
	for i := 0; i < 5000; i++ {
		// Multiples of 125ms keep every refill and leak amount exactly
		// representable, so any disagreement would be a real difference in
		// behavior, not floating point noise.
		now = now.Add(time.Duration(rng.Intn(9)) * 125 * time.Millisecond)
		a, b := tb.Allow("client"), lb.Allow("client")
		if a != b {
			t.Fatalf("request %d: token bucket said %v, leaky bucket meter said %v", i, a, b)
		}
		if a {
			accepted++
		} else {
			rejected++
		}
	}
	if accepted == 0 || rejected == 0 {
		t.Fatalf("the comparison is only meaningful if both outcomes occur, got %d accepted and %d rejected", accepted, rejected)
	}
}
