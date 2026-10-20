// Package timeline implements the three ways to build a home timeline from
// the people a user follows, so they can be measured against each other
// instead of argued about.
//
//	FanoutOnWrite   push each post into every follower's precomputed inbox
//	FanoutOnRead    store the post once, assemble the timeline at read time
//	Hybrid          push for ordinary authors, assemble at read time for
//	                celebrities, the approach real feeds converge on
package timeline

import (
	"sync"

	"github.com/Sumit6258/Faultline/systems/social-feed/internal/graph"
	"github.com/Sumit6258/Faultline/systems/social-feed/internal/post"
)

// Strategy is how a post reaches the timelines of the people who follow
// its author. OnPost is called after the post is already in the post
// store. GetTimeline returns up to limit posts, newest first.
type Strategy interface {
	OnPost(p *post.Post)
	GetTimeline(user string, limit int) []*post.Post

	// InboxWrites is the total number of per-follower inbox writes this
	// strategy has performed. It is the fan-out amplification: one post
	// by an author with N followers costs N writes under fan-out on
	// write, and none under fan-out on read.
	InboxWrites() int64
}

// FanoutOnWrite does the work when a post is created: one inbox write per
// follower. Reading a timeline afterward is a single bounded read of the
// reader's own inbox, as cheap as a read gets. The cost is on the write
// side, and it scales with the author's follower count, which is exactly
// what goes wrong for an author with millions of them.
type FanoutOnWrite struct {
	graph   *graph.Graph
	inboxes *inboxes
}

func NewFanoutOnWrite(g *graph.Graph, maxInbox int) *FanoutOnWrite {
	return &FanoutOnWrite{graph: g, inboxes: newInboxes(maxInbox)}
}

func (f *FanoutOnWrite) OnPost(p *post.Post) {
	for _, follower := range f.graph.Followers(p.Author) {
		f.inboxes.push(follower, p)
	}
}

func (f *FanoutOnWrite) GetTimeline(user string, limit int) []*post.Post {
	return f.inboxes.read(user, limit)
}

func (f *FanoutOnWrite) InboxWrites() int64 { return f.inboxes.writes.Load() }

// FanoutOnRead does nothing when a post is created, the post is already in
// the post store, and does the work when a timeline is requested: look up
// everyone the reader follows, take each one's recent posts, and merge.
// Writes cost the same for every author no matter how many followers they
// have. The cost moves to reads, and scales with how many people the
// reader follows, paid again on every single refresh.
type FanoutOnRead struct {
	graph *graph.Graph
	posts *post.Store
}

func NewFanoutOnRead(g *graph.Graph, posts *post.Store) *FanoutOnRead {
	return &FanoutOnRead{graph: g, posts: posts}
}

func (f *FanoutOnRead) OnPost(p *post.Post) {}

func (f *FanoutOnRead) InboxWrites() int64 { return 0 }

func (f *FanoutOnRead) GetTimeline(user string, limit int) []*post.Post {
	following := f.graph.Following(user)
	lists := make([][]*post.Post, 0, len(following))
	for _, followee := range following {
		lists = append(lists, f.posts.ByAuthor(followee, limit))
	}
	return mergeNewestFirst(lists, limit)
}

// Hybrid pushes posts from ordinary authors into follower inboxes, as
// FanoutOnWrite does, but never pushes a celebrity's posts. A celebrity is
// an author whose follower count reaches Threshold when they post. At read
// time, the reader's inbox is merged with the recent posts of just the
// celebrities they follow, which is a handful of lookups no matter how
// many followers each of those celebrities has.
//
// Celebrity status is sticky: once an author is treated as a celebrity
// they stay one, even if their follower count later falls below the
// threshold. Recomputing it from the current follower count at read time
// would silently drop every post made while they were above it, since
// those were never pushed to any inbox. The opposite direction is safe: an
// author who crosses the threshold upward has older posts already pushed
// into inboxes and newer ones served at read time, and the merge emits
// each post once.
type Hybrid struct {
	graph     *graph.Graph
	posts     *post.Store
	inboxes   *inboxes
	threshold int

	mu          sync.RWMutex
	celebrities map[string]struct{}
}

func NewHybrid(g *graph.Graph, posts *post.Store, maxInbox, celebrityThreshold int) *Hybrid {
	return &Hybrid{
		graph:       g,
		posts:       posts,
		inboxes:     newInboxes(maxInbox),
		threshold:   celebrityThreshold,
		celebrities: make(map[string]struct{}),
	}
}

func (h *Hybrid) isCelebrity(author string) bool {
	h.mu.RLock()
	_, ok := h.celebrities[author]
	h.mu.RUnlock()
	return ok
}

func (h *Hybrid) OnPost(p *post.Post) {
	if !h.isCelebrity(p.Author) && h.graph.FollowerCount(p.Author) >= h.threshold {
		h.mu.Lock()
		h.celebrities[p.Author] = struct{}{}
		h.mu.Unlock()
	}
	if h.isCelebrity(p.Author) {
		return
	}
	for _, follower := range h.graph.Followers(p.Author) {
		h.inboxes.push(follower, p)
	}
}

func (h *Hybrid) InboxWrites() int64 { return h.inboxes.writes.Load() }

func (h *Hybrid) GetTimeline(user string, limit int) []*post.Post {
	lists := [][]*post.Post{h.inboxes.read(user, limit)}

	h.mu.RLock()
	for _, followee := range h.graph.Following(user) {
		if _, ok := h.celebrities[followee]; ok {
			lists = append(lists, h.posts.ByAuthor(followee, limit))
		}
	}
	h.mu.RUnlock()

	return mergeNewestFirst(lists, limit)
}
