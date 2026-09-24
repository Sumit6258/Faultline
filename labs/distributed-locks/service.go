package distlock

import (
	"sync"
	"time"
)

// Service grants time bounded leases on named resources, each backed by a
// monotonically increasing fencing token. A lease means you may hold this
// lock until it expires, not that you hold it forever: if the holder
// crashes or stalls past its lease, another client can take over. The
// fencing token exists so a protected resource can detect and reject a
// write from a holder whose lease already expired, even if that holder
// doesn't know it yet. See fenced_resource.go and the README.
type Service struct {
	mu        sync.Mutex
	locks     map[string]*leaseState
	nextToken int64
	clock     Clock
}

type leaseState struct {
	holder    string
	token     int64
	expiresAt time.Time
}

func NewService() *Service {
	return &Service{
		locks: make(map[string]*leaseState),
		clock: realClock,
	}
}

// TryAcquire grants holder a lease on resource for ttl, if nobody else
// currently holds an unexpired lease on it, and returns a fencing token
// higher than any token issued before. ok is false if someone else holds
// an unexpired lease.
func (s *Service) TryAcquire(resource, holder string, ttl time.Duration) (token int64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock()
	existing, has := s.locks[resource]
	if has && existing.holder != holder && now.Before(existing.expiresAt) {
		return 0, false
	}

	s.nextToken++
	s.locks[resource] = &leaseState{
		holder:    holder,
		token:     s.nextToken,
		expiresAt: now.Add(ttl),
	}
	return s.nextToken, true
}

// Renew extends holder's lease on resource, as long as it still holds the
// current token.
func (s *Service) Renew(resource, holder string, token int64, ttl time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, has := s.locks[resource]
	if !has || existing.holder != holder || existing.token != token {
		return false
	}
	existing.expiresAt = s.clock().Add(ttl)
	return true
}

// Release gives up the lease early, if the caller still holds it.
func (s *Service) Release(resource, holder string, token int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, has := s.locks[resource]
	if !has || existing.holder != holder || existing.token != token {
		return false
	}
	delete(s.locks, resource)
	return true
}
