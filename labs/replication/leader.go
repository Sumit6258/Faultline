package replication

import (
	"sync"
	"time"
)

// LogEntry is one write, in the order the leader accepted it.
type LogEntry struct {
	Sequence  int64
	Key       string
	Value     string
	WrittenAt time.Time
}

// Leader accepts writes and keeps the single, ordered, authoritative log of
// them. It has no concept of followers. It just appends, and lets anyone
// ask for every entry after a given sequence number, which is what a
// follower polls for. See the README for why this lab simulates
// replication instead of using a real database: this sandbox can't reliably
// keep a real Postgres primary and standby running across tool calls.
type Leader struct {
	mu    sync.Mutex
	log   []LogEntry
	seq   int64
	clock Clock
}

func NewLeader() *Leader {
	return &Leader{clock: realClock}
}

func (l *Leader) Write(key, value string) LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	entry := LogEntry{Sequence: l.seq, Key: key, Value: value, WrittenAt: l.clock()}
	l.log = append(l.log, entry)
	return entry
}

// EntriesSince returns every log entry with Sequence greater than after, in
// order.
func (l *Leader) EntriesSince(after int64) []LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []LogEntry
	for _, e := range l.log {
		if e.Sequence > after {
			out = append(out, e)
		}
	}
	return out
}

func (l *Leader) LatestSequence() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.seq
}

// SetClock overrides the leader's time source. Exported so callers outside
// this package, such as the demo, can drive the leader and a follower off
// the same shared fake clock instead of waiting on real time.
func (l *Leader) SetClock(c Clock) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.clock = c
}
