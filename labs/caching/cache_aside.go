package cache

// CacheAsideStore wraps a slow upstream with an LRU cache using the
// cache-aside pattern: check the cache first, and on a miss, read the
// upstream, store the result, then return it. Concurrent misses for the
// same key are coalesced so a stampede only reaches the upstream once.
type CacheAsideStore struct {
	cache     *LRU
	source    *SlowStore
	coalescer *Coalescer
}

func NewCacheAsideStore(cache *LRU, source *SlowStore) *CacheAsideStore {
	return &CacheAsideStore{
		cache:     cache,
		source:    source,
		coalescer: NewCoalescer(),
	}
}

func (c *CacheAsideStore) Get(key string) (string, bool) {
	if v, ok := c.cache.Get(key); ok {
		return v, true
	}

	return c.coalescer.Do(key, func() (string, bool) {
		// Check the cache again here. Another goroutine may have already
		// populated it while this one was waiting for the coalescer's lock.
		if v, ok := c.cache.Get(key); ok {
			return v, true
		}
		v, ok := c.source.Get(key)
		if ok {
			c.cache.Set(key, v)
		}
		return v, ok
	})
}
