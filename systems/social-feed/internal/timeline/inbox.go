package timeline

import (
	"hash/fnv"
	"sync"
	"sync/atomic"

	"github.com/Sumit6258/Faultline/systems/social-feed/internal/post"
)

const shardCount = 64

// inboxes holds one bounded, precomputed timeline per user, split across
// shards so that fanning one post out to many followers doesn't serialize
// every write behind a single lock.
//
// Each inbox is a slice kept oldest first, with the newest entry at the
// end. It is allowed to grow to twice max before being trimmed back to the
// newest max entries, which makes a push amortized O(1): one O(max) copy
// per max pushes, instead of an O(max) copy on every push that prepending
// to a slice would cost. A cost model where fanning out to a follower is
// one cheap write is the honest one; real timeline stores, a Redis list
// with a trim, behave that way.
type inboxes struct {
	max    int
	shards [shardCount]shard
	writes atomic.Int64 // every push, the fan-out amplification a post causes
}

type shard struct {
	mu sync.RWMutex
	m  map[string][]*post.Post
}

func newInboxes(max int) *inboxes {
	in := &inboxes{max: max}
	for i := range in.shards {
		in.shards[i].m = make(map[string][]*post.Post)
	}
	return in
}

func (in *inboxes) shardFor(user string) *shard {
	h := fnv.New32a()
	h.Write([]byte(user))
	return &in.shards[h.Sum32()%shardCount]
}

func (in *inboxes) push(user string, p *post.Post) {
	in.writes.Add(1)
	s := in.shardFor(user)
	s.mu.Lock()
	box := append(s.m[user], p)
	if len(box) > 2*in.max {
		kept := make([]*post.Post, in.max, 2*in.max)
		copy(kept, box[len(box)-in.max:])
		box = kept
	}
	s.m[user] = box
	s.mu.Unlock()
}

// read returns up to limit of user's newest posts, newest first, never
// more than the inbox's logical bound max.
func (in *inboxes) read(user string, limit int) []*post.Post {
	if limit > in.max {
		limit = in.max
	}
	s := in.shardFor(user)
	s.mu.RLock()
	defer s.mu.RUnlock()
	box := s.m[user]
	n := len(box)
	if n > limit {
		n = limit
	}
	out := make([]*post.Post, 0, n)
	for i := len(box) - 1; i >= len(box)-n; i-- {
		out = append(out, box[i])
	}
	return out
}
