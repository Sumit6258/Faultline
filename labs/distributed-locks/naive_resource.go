package distlock

import "sync"

// NaiveResource has no fencing at all: any write succeeds, regardless of
// who sends it or in what order it arrives. It exists only to demonstrate
// what FencedResource prevents. See the README's Failure Modes section.
type NaiveResource struct {
	mu    sync.Mutex
	value string
}

func NewNaiveResource() *NaiveResource {
	return &NaiveResource{}
}

func (r *NaiveResource) Write(value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.value = value
}

func (r *NaiveResource) Value() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.value
}
