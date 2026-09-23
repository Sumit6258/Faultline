package ratelimit

// Limiter decides whether a request identified by key is allowed right now.
// Every algorithm in this package implements it the same way so they can be
// swapped without touching the caller.
type Limiter interface {
	Allow(key string) bool
}
