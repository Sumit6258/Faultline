package urlshortener

import (
	"errors"
	"sync/atomic"

	"github.com/Sumit6258/Faultline/systems/url-shortener/internal/analytics"
	"github.com/Sumit6258/Faultline/systems/url-shortener/internal/cache"
	"github.com/Sumit6258/Faultline/systems/url-shortener/internal/idgen"
	"github.com/Sumit6258/Faultline/systems/url-shortener/internal/ratelimit"
	"github.com/Sumit6258/Faultline/systems/url-shortener/internal/store"
)

// ErrRateLimited is returned by Shorten when the calling client has
// exceeded its creation rate.
var ErrRateLimited = errors.New("urlshortener: rate limited")

// Service is the URL shortener itself: it wires an ID generator, a store, a
// cache in front of that store, a per-client rate limiter for abuse
// prevention, and an asynchronous click collector into the two operations
// that matter, Shorten and Resolve. This is the V0 to V2 slice of the
// naive-to-production evolution described in the README: a single process,
// no persistence beyond memory, but already cached, abuse-guarded, and
// asynchronous where it counts.
type Service struct {
	idgen     idgen.Generator
	store     store.Store
	cache     *cache.LRU
	limiter   *ratelimit.Limiter
	analytics *analytics.Collector
	cacheHits atomic.Int64
	cacheMiss atomic.Int64
}

func New(gen idgen.Generator, st store.Store, cacheCapacity int, rateCapacity, rateRefillPerSecond float64, analyticsBuffer int) *Service {
	return &Service{
		idgen:     gen,
		store:     st,
		cache:     cache.NewLRU(cacheCapacity),
		limiter:   ratelimit.NewLimiter(rateCapacity, rateRefillPerSecond),
		analytics: analytics.NewCollector(analyticsBuffer),
	}
}

// Shorten creates a new short code for longURL, on behalf of clientID (used
// only for rate limiting abuse prevention).
func (s *Service) Shorten(clientID, longURL string) (code string, err error) {
	if !s.limiter.Allow(clientID) {
		return "", ErrRateLimited
	}

	code, err = s.idgen.Generate()
	if err != nil {
		return "", err
	}
	if err := s.store.Save(code, longURL); err != nil {
		return "", err
	}
	return code, nil
}

// Resolve returns the long URL for code, checking the cache before falling
// back to the store, and records a click asynchronously on a hit.
func (s *Service) Resolve(code string) (longURL string, ok bool) {
	if v, hit := s.cache.Get(code); hit {
		s.cacheHits.Add(1)
		s.analytics.RecordClick(code)
		return v, true
	}
	s.cacheMiss.Add(1)

	v, found := s.store.Load(code)
	if found {
		s.cache.Set(code, v)
		s.analytics.RecordClick(code)
	}
	return v, found
}

// Stats reports how many Resolve calls were served from cache versus the
// store, so cache effectiveness can be measured directly instead of
// inferred.
type Stats struct {
	CacheHits   int64
	CacheMisses int64
}

func (s *Service) Stats() Stats {
	return Stats{CacheHits: s.cacheHits.Load(), CacheMisses: s.cacheMiss.Load()}
}

func (s *Service) Clicks(code string) int64 {
	return s.analytics.Count(code)
}

// Stop shuts down background work, currently just the analytics collector.
func (s *Service) Stop() {
	s.analytics.Stop()
}
