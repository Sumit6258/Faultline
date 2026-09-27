package ratelimit

import (
	"sync"
	"time"
)

// Limiter is a per-client token bucket used to keep any single client from
// creating an unbounded number of short URLs, which is this system's abuse
// prevention: without it, one script could flood the store with junk
// entries at no cost to itself. This is a fresh, minimal implementation
// for this system rather than a shared dependency on labs/rate-limiting,
// every lab and system in this repository stays independently buildable
// on its own.
type Limiter struct {
	mu         sync.Mutex
	capacity   float64
	refillRate float64
	buckets    map[string]*bucket
}

type bucket struct {
	tokens     float64
	lastRefill time.Time
}

func NewLimiter(capacity, refillPerSecond float64) *Limiter {
	return &Limiter{
		capacity:   capacity,
		refillRate: refillPerSecond,
		buckets:    make(map[string]*bucket),
	}
}

func (l *Limiter) Allow(clientID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[clientID]
	if !ok {
		l.buckets[clientID] = &bucket{tokens: l.capacity - 1, lastRefill: now}
		return true
	}

	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens = min(l.capacity, b.tokens+elapsed*l.refillRate)
	b.lastRefill = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
