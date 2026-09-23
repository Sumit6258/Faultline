package cache

import "sync"

// Coalescer ensures that concurrent calls to Do for the same key result in
// exactly one execution of fn, with every caller waiting on that key
// getting the same result. This is what stops a cache stampede: when a hot
// key expires, a flood of requests that all miss the cache at once still
// only reaches the database once, not once per request.
type Coalescer struct {
	mu    sync.Mutex
	calls map[string]*inflightCall
}

type inflightCall struct {
	wg    sync.WaitGroup
	value string
	found bool
}

func NewCoalescer() *Coalescer {
	return &Coalescer{calls: make(map[string]*inflightCall)}
}

// Do runs fn for key if no call for that key is already in flight, and
// returns its result. If a call for key is already running, Do waits for it
// and returns the same result instead of running fn again.
func (c *Coalescer) Do(key string, fn func() (string, bool)) (string, bool) {
	c.mu.Lock()
	if call, ok := c.calls[key]; ok {
		c.mu.Unlock()
		call.wg.Wait()
		return call.value, call.found
	}

	call := &inflightCall{}
	call.wg.Add(1)
	c.calls[key] = call
	c.mu.Unlock()

	call.value, call.found = fn()
	call.wg.Done()

	c.mu.Lock()
	delete(c.calls, key)
	c.mu.Unlock()

	return call.value, call.found
}
