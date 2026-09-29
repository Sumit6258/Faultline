package ratelimit

import (
	"sync"
	"time"
)

// LeakyBucketQueue is the leaky bucket as a shaper: accepted requests wait
// in a queue and are released at a constant rate, one per interval, no
// matter how bursty the arrivals were. That is what distinguishes a leaky
// bucket from a token bucket. The LeakyBucket type in this package is the
// other reading, a meter that rejects instead of queueing, and it accepts
// exactly the same requests a TokenBucket does, see equivalence_test.go.
//
// capacity is how many requests may be waiting at once. One more can be
// released immediately, so an instantaneous burst is accepted up to
// capacity+1 requests and the rest are dropped.
type LeakyBucketQueue struct {
	mu       sync.Mutex
	capacity int
	interval time.Duration
	state    map[string]*queueState
	clock    Clock
}

type queueState struct {
	pending []time.Time // release times still in the future, oldest first
	last    time.Time   // release time of the most recently accepted request
	seen    bool
}

func NewLeakyBucketQueue(capacity int, interval time.Duration) *LeakyBucketQueue {
	return &LeakyBucketQueue{
		capacity: capacity,
		interval: interval,
		state:    make(map[string]*queueState),
		clock:    realClock,
	}
}

// Admit reports how long the caller must wait before the request may
// proceed, and false if the queue is full and the request is dropped.
func (q *LeakyBucketQueue) Admit(key string) (wait time.Duration, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := q.clock()
	st, exists := q.state[key]
	if !exists {
		st = &queueState{}
		q.state[key] = st
	}

	released := 0
	for released < len(st.pending) && !st.pending[released].After(now) {
		released++
	}
	st.pending = st.pending[released:]

	if len(st.pending) >= q.capacity {
		return 0, false
	}

	release := now
	if st.seen {
		if next := st.last.Add(q.interval); next.After(now) {
			release = next
		}
	}
	st.pending = append(st.pending, release)
	st.last = release
	st.seen = true
	return release.Sub(now), true
}
