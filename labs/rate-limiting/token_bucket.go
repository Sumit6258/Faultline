package ratelimit

import (
	"sync"
	"time"
)

// TokenBucket allows short bursts up to capacity tokens, refilling at
// refillRate tokens per second. A client that has been idle can save up
// burst capacity, which the window algorithms don't allow.
type TokenBucket struct {
	mu         sync.Mutex
	capacity   float64
	refillRate float64
	buckets    map[string]*tokenState
	clock      Clock
}

type tokenState struct {
	tokens     float64
	lastRefill time.Time
}

func NewTokenBucket(capacity float64, refillRate float64) *TokenBucket {
	return &TokenBucket{
		capacity:   capacity,
		refillRate: refillRate,
		buckets:    make(map[string]*tokenState),
		clock:      realClock,
	}
}

func (t *TokenBucket) Allow(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.clock()
	st, ok := t.buckets[key]
	if !ok {
		st = &tokenState{tokens: t.capacity - 1, lastRefill: now}
		t.buckets[key] = st
		return true
	}

	elapsed := now.Sub(st.lastRefill).Seconds()
	st.tokens = min(t.capacity, st.tokens+elapsed*t.refillRate)
	st.lastRefill = now

	if st.tokens < 1 {
		return false
	}
	st.tokens--
	return true
}
