package leaderelection

import (
	"sync"
	"time"
)

// Cluster tracks which node is currently the leader by way of heartbeats.
// Any node can call Heartbeat. It becomes leader if there currently is
// none, or if the current leader has gone silent for longer than timeout.
// A node calling Heartbeat while a different node is leader and still
// within the timeout is refused: only one node can be leader within any
// timeout window. Term increases by one every time leadership changes
// hands, so callers can tell a genuinely new leadership from a renewed one.
type Cluster struct {
	mu       sync.Mutex
	leader   string
	term     int64
	lastBeat time.Time
	timeout  time.Duration
	clock    Clock
}

func NewCluster(timeout time.Duration) *Cluster {
	return &Cluster{timeout: timeout, clock: realClock}
}

// Heartbeat is meant to be called periodically by every node. It returns
// the current term and whether node is the leader for that term.
func (c *Cluster) Heartbeat(node string) (term int64, isLeader bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.clock()
	leaderIsSilent := c.leader == "" || now.Sub(c.lastBeat) > c.timeout

	if c.leader == node && !leaderIsSilent {
		c.lastBeat = now
		return c.term, true
	}

	if leaderIsSilent {
		c.term++
		c.leader = node
		c.lastBeat = now
		return c.term, true
	}

	return c.term, false
}

// Leader returns the current leader and term without sending a heartbeat.
func (c *Cluster) Leader() (node string, term int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.leader, c.term
}
