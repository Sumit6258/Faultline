package urlshortener

import (
	"testing"
	"time"

	"github.com/Sumit6258/Faultline/systems/url-shortener/internal/idgen"
	"github.com/Sumit6258/Faultline/systems/url-shortener/internal/store"
)

// countingStore wraps store.Memory and counts Load calls, so tests can
// prove the cache is actually being used instead of just hoping it is.
type countingStore struct {
	*store.Memory
	loads int
}

func newCountingStore() *countingStore {
	return &countingStore{Memory: store.NewMemory()}
}

func (c *countingStore) Load(code string) (string, bool) {
	c.loads++
	return c.Memory.Load(code)
}

func newTestService() (*Service, *countingStore) {
	st := newCountingStore()
	svc := New(&idgen.Counter{}, st, 100, 100, 100, 100)
	return svc, st
}

func TestService_ShortenAndResolve(t *testing.T) {
	svc, _ := newTestService()
	defer svc.Stop()

	code, err := svc.Shorten("client-1", "https://example.com/page")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, ok := svc.Resolve(code)
	if !ok || got != "https://example.com/page" {
		t.Fatalf("expected to resolve back the original URL, got %q ok=%v", got, ok)
	}
}

func TestService_ResolveMissingCode(t *testing.T) {
	svc, _ := newTestService()
	defer svc.Stop()

	_, ok := svc.Resolve("does-not-exist")
	if ok {
		t.Fatal("expected a miss for a code that was never shortened")
	}
}

func TestService_SecondResolveHitsCacheNotStore(t *testing.T) {
	svc, st := newTestService()
	defer svc.Stop()

	code, _ := svc.Shorten("client-1", "https://example.com")
	svc.Resolve(code) // first resolve, a cache miss, hits the store
	loadsAfterFirst := st.loads

	svc.Resolve(code) // second resolve, should be a cache hit
	if st.loads != loadsAfterFirst {
		t.Fatalf("expected the second resolve to hit the cache and not call Load again, loads went from %d to %d", loadsAfterFirst, st.loads)
	}
}

func TestService_RateLimitBlocksExcessCreation(t *testing.T) {
	st := newCountingStore()
	svc := New(&idgen.Counter{}, st, 100, 2, 0.001, 100) // capacity 2, near zero refill
	defer svc.Stop()

	svc.Shorten("client-1", "https://a.example.com")
	svc.Shorten("client-1", "https://b.example.com")
	_, err := svc.Shorten("client-1", "https://c.example.com")

	if err != ErrRateLimited {
		t.Fatalf("expected the 3rd creation from the same client to be rate limited, got %v", err)
	}
}

func TestService_RateLimitIsPerClient(t *testing.T) {
	st := newCountingStore()
	svc := New(&idgen.Counter{}, st, 100, 1, 0.001, 100)
	defer svc.Stop()

	svc.Shorten("client-1", "https://a.example.com")
	_, err := svc.Shorten("client-2", "https://b.example.com")
	if err != nil {
		t.Fatalf("expected client-2's first request to succeed independent of client-1's usage, got %v", err)
	}
}

func TestService_ClicksIncrementOnResolve(t *testing.T) {
	svc, _ := newTestService()
	defer svc.Stop()

	code, _ := svc.Shorten("client-1", "https://example.com")
	svc.Resolve(code)
	svc.Resolve(code)
	svc.Resolve(code)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && svc.Clicks(code) != 3 {
		time.Sleep(time.Millisecond)
	}
	if got := svc.Clicks(code); got != 3 {
		t.Fatalf("expected 3 clicks recorded, got %d", got)
	}
}

func TestService_StatsTrackHitsAndMisses(t *testing.T) {
	svc, _ := newTestService()
	defer svc.Stop()

	code, _ := svc.Shorten("client-1", "https://example.com")
	svc.Resolve(code) // miss, first read goes to the store
	svc.Resolve(code) // hit
	svc.Resolve(code) // hit
	svc.Resolve("nonexistent-code")

	stats := svc.Stats()
	if stats.CacheMisses != 2 {
		t.Fatalf("expected 2 misses (first real read plus the nonexistent code), got %d", stats.CacheMisses)
	}
	if stats.CacheHits != 2 {
		t.Fatalf("expected 2 hits, got %d", stats.CacheHits)
	}
}
