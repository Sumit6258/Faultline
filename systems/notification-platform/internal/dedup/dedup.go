// Package dedup stops the same notification from being submitted twice. A
// caller retrying a timed-out "send this notification" API call is the
// same problem labs/distributed-locks and systems/payment-platform solve
// for other kinds of duplicate request, applied here to notifications
// instead of locks or charges.
package dedup

import "sync"

type Tracker struct {
	mu   sync.Mutex
	seen map[string]bool
}

func NewTracker() *Tracker {
	return &Tracker{seen: make(map[string]bool)}
}

// Claim reports whether id has not been seen before, and marks it seen if
// so. The check and the mark happen under the same lock, which is what
// makes this safe when two submissions with the same id race: exactly one
// of them gets claimed=true, no matter how close together they arrive.
func (t *Tracker) Claim(id string) (claimed bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.seen[id] {
		return false
	}
	t.seen[id] = true
	return true
}
