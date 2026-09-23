package ratelimit

import (
	"sync"
	"time"
)

// LeakyBucket queues requests into a bucket of fixed capacity that leaks
// (drains) at a constant rate. Unlike TokenBucket it smooths a burst into a
// steady outflow instead of letting the whole burst through immediately.
type LeakyBucket struct {
	mu       sync.Mutex
	capacity float64
	leakRate float64
	buckets  map[string]*leakyState
	clock    Clock
}

type leakyState struct {
	level    float64
	lastLeak time.Time
}

func NewLeakyBucket(capacity float64, leakRate float64) *LeakyBucket {
	return &LeakyBucket{
		capacity: capacity,
		leakRate: leakRate,
		buckets:  make(map[string]*leakyState),
		clock:    realClock,
	}
}

func (l *LeakyBucket) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock()
	st, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &leakyState{level: 1, lastLeak: now}
		return true
	}

	elapsed := now.Sub(st.lastLeak).Seconds()
	st.level = max(0, st.level-elapsed*l.leakRate)
	st.lastLeak = now

	if st.level+1 > l.capacity {
		return false
	}
	st.level++
	return true
}
