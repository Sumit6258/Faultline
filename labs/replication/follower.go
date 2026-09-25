package replication

import (
	"sync"
	"time"
)

// Follower asynchronously replicates a Leader's log. Sync pulls and applies
// any entries that are old enough, according to lag, to be visible to this
// follower. lag stands in for everything that delays a real replica: the
// network, WAL shipping, apply time. A write is invisible to this follower
// until lag has passed since it was written, even if Sync is called
// immediately after the write. See the README for what this demonstrates.
type Follower struct {
	mu         sync.RWMutex
	leader     *Leader
	state      map[string]string
	appliedSeq int64
	lag        time.Duration
	clock      Clock
}

func NewFollower(leader *Leader, lag time.Duration) *Follower {
	return &Follower{
		leader: leader,
		state:  make(map[string]string),
		lag:    lag,
		clock:  realClock,
	}
}

// Sync applies every entry from the leader that is at least lag old. It
// stops at the first entry that's still too recent, since entries are
// applied in order and everything after that one is even more recent.
// Returns how many entries were newly applied.
func (f *Follower) Sync() int {
	now := f.clock()
	entries := f.leader.EntriesSince(f.appliedSeq)

	f.mu.Lock()
	defer f.mu.Unlock()

	applied := 0
	for _, e := range entries {
		if now.Sub(e.WrittenAt) < f.lag {
			break
		}
		f.state[e.Key] = e.Value
		f.appliedSeq = e.Sequence
		applied++
	}
	return applied
}

func (f *Follower) Read(key string) (string, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	v, ok := f.state[key]
	return v, ok
}

func (f *Follower) AppliedSeq() int64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.appliedSeq
}

// LagBehind reports how many of the leader's committed entries this
// follower has not yet applied.
func (f *Follower) LagBehind() int64 {
	f.mu.RLock()
	applied := f.appliedSeq
	f.mu.RUnlock()
	return f.leader.LatestSequence() - applied
}

// SetClock overrides the follower's time source. See Leader.SetClock.
func (f *Follower) SetClock(c Clock) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clock = c
}
