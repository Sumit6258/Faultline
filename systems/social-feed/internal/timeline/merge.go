package timeline

import (
	"container/heap"

	"github.com/Sumit6258/Faultline/systems/social-feed/internal/post"
)

// newer is the single ordering used everywhere a timeline is assembled:
// later CreatedAt first, ties broken by creation sequence. Seq is unique,
// so this is a total order, which is what makes duplicate detection in
// mergeNewestFirst a simple adjacent comparison.
func newer(a, b *post.Post) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.After(b.CreatedAt)
	}
	return a.Seq > b.Seq
}

type cursor struct {
	list []*post.Post
	idx  int
}

type mergeHeap []*cursor

func (h mergeHeap) Len() int           { return len(h) }
func (h mergeHeap) Less(i, j int) bool { return newer(h[i].list[h[i].idx], h[j].list[h[j].idx]) }
func (h mergeHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *mergeHeap) Push(x any)        { *h = append(*h, x.(*cursor)) }
func (h *mergeHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// mergeNewestFirst k-way merges lists, each already sorted newest first,
// into one list of at most limit posts. A post that appears in more than
// one input list is emitted once. Cost is O(k + limit*log k) rather than
// the O(k*limit*log(k*limit)) of concatenating and sorting, which matters
// here: fan-out on read pays this on every single read, so a deliberately
// naive merge would make that strategy look worse than it fundamentally is.
func mergeNewestFirst(lists [][]*post.Post, limit int) []*post.Post {
	h := make(mergeHeap, 0, len(lists))
	for _, l := range lists {
		if len(l) > 0 {
			h = append(h, &cursor{list: l})
		}
	}
	heap.Init(&h)

	out := make([]*post.Post, 0, limit)
	var lastSeq int64 = -1
	for len(out) < limit && h.Len() > 0 {
		c := h[0]
		p := c.list[c.idx]
		if p.Seq != lastSeq {
			out = append(out, p)
			lastSeq = p.Seq
		}
		c.idx++
		if c.idx >= len(c.list) {
			heap.Pop(&h)
		} else {
			heap.Fix(&h, 0)
		}
	}
	return out
}
