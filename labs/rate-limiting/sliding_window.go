package ratelimit

import (
	"sync"
	"time"
)

// SlidingWindowCounter approximates a true sliding window by weighting the
// previous fixed window's count by how much of it still overlaps the
// current moment. It costs one counter per window instead of one entry per
// request, which is what a sliding log would need, and it smooths out most
// of FixedWindow's boundary burst problem without that memory cost.
type SlidingWindowCounter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	buckets map[string]*slidingState
	clock   Clock
}

type slidingState struct {
	windowStart time.Time
	currCount   int
	prevCount   int
}

func NewSlidingWindowCounter(limit int, window time.Duration) *SlidingWindowCounter {
	return &SlidingWindowCounter{
		limit:   limit,
		window:  window,
		buckets: make(map[string]*slidingState),
		clock:   realClock,
	}
}

func (s *SlidingWindowCounter) Allow(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock()
	currentWindowStart := now.Truncate(s.window)

	st, ok := s.buckets[key]
	if !ok {
		st = &slidingState{windowStart: currentWindowStart}
		s.buckets[key] = st
	} else if currentWindowStart.After(st.windowStart) {
		windowsPassed := currentWindowStart.Sub(st.windowStart) / s.window
		if windowsPassed == 1 {
			st.prevCount = st.currCount
		} else {
			st.prevCount = 0
		}
		st.currCount = 0
		st.windowStart = currentWindowStart
	}

	elapsedFraction := float64(now.Sub(st.windowStart)) / float64(s.window)
	estimated := float64(st.prevCount)*(1-elapsedFraction) + float64(st.currCount)

	if estimated >= float64(s.limit) {
		return false
	}
	st.currCount++
	return true
}
