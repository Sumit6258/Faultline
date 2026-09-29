package ratelimit

import (
	"sync"
	"time"
)

// LeakyBucket is the meter reading of a leaky bucket: a bucket of fixed
// capacity that drains at a constant rate, where a request is rejected if
// it would overflow. Nothing is queued and nothing is delayed, so it
// accepts exactly the same requests a TokenBucket does, the water level is
// just capacity minus the tokens left. equivalence_test.go measures that.
// For the reading that actually smooths bursts, by holding requests in a
// queue and releasing them at a constant rate, see LeakyBucketQueue.
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
