package senders

import (
	"errors"
	"sync"
	"time"
)

// ErrRateLimited is returned when a channel's own provider rate limit has
// been exceeded. Real providers (an SMS gateway, an email API) enforce
// limits like this on their side regardless of what this system wants to
// send; RateLimited lets this system respect one before a real provider
// would reject the call outright.
var ErrRateLimited = errors.New("senders: channel rate limit exceeded")

// RateLimited wraps a Sender with a token bucket, a fresh implementation
// for this system rather than a shared import, every lab and system in
// this repository stays independently buildable on its own.
type RateLimited struct {
	mu         sync.Mutex
	inner      Sender
	capacity   float64
	refillRate float64
	tokens     float64
	lastRefill time.Time
	clock      func() time.Time
}

func NewRateLimited(inner Sender, capacity, refillPerSecond float64) *RateLimited {
	return &RateLimited{inner: inner, capacity: capacity, refillRate: refillPerSecond, tokens: capacity, clock: time.Now}
}

func (r *RateLimited) Send(recipient, body string) error {
	r.mu.Lock()
	now := r.clock()
	if r.lastRefill.IsZero() {
		r.lastRefill = now
	}
	elapsed := now.Sub(r.lastRefill).Seconds()
	r.tokens = min(r.capacity, r.tokens+elapsed*r.refillRate)
	r.lastRefill = now
	if r.tokens < 1 {
		r.mu.Unlock()
		return ErrRateLimited
	}
	r.tokens--
	r.mu.Unlock()

	return r.inner.Send(recipient, body)
}
