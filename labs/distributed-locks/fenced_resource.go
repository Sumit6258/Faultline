package distlock

import "sync"

// FencedResource stands in for anything a lock is meant to protect: a
// file, a database row, a piece of hardware. Write only succeeds if token
// is at least as high as the highest token this resource has ever seen.
// That's what makes a stale lock holder's write fail safely instead of
// silently corrupting the resource: even if that holder doesn't know its
// lease expired, the resource does, because it already saw a higher token
// from whoever took over.
type FencedResource struct {
	mu           sync.Mutex
	highestToken int64
	value        string
}

func NewFencedResource() *FencedResource {
	return &FencedResource{}
}

func (r *FencedResource) Write(token int64, value string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if token < r.highestToken {
		return false
	}
	r.highestToken = token
	r.value = value
	return true
}

func (r *FencedResource) Value() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.value
}
