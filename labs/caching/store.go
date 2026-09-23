package cache

import (
	"sync"
	"sync/atomic"
	"time"
)

// SlowStore simulates a database or other slow upstream. Every Get sleeps
// for latency before returning, and Calls reports how many times Get
// actually ran, so tests and benchmarks can prove how many times the
// "database" was hit instead of just trusting the cache.
type SlowStore struct {
	mu      sync.RWMutex
	data    map[string]string
	latency time.Duration
	calls   atomic.Int64
}

func NewSlowStore(latency time.Duration) *SlowStore {
	return &SlowStore{
		data:    make(map[string]string),
		latency: latency,
	}
}

func (s *SlowStore) Seed(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
}

func (s *SlowStore) Get(key string) (string, bool) {
	s.calls.Add(1)
	time.Sleep(s.latency)
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	return v, ok
}

func (s *SlowStore) Calls() int64 {
	return s.calls.Load()
}
