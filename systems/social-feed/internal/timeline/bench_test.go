package timeline

import (
	"fmt"
	"strconv"
	"sync"
	"testing"

	"github.com/Sumit6258/Faultline/systems/social-feed/internal/graph"
	"github.com/Sumit6258/Faultline/systems/social-feed/internal/post"
)

// benchWorld is one reader who follows F ordinary authors and 3
// celebrities. Every author has posted 40 times, interleaved, and every
// strategy has been handed every post, so all three hold the same timeline
// state. The 3 celebrities each have exactly celebrityFollowers followers,
// which meets the hybrid's threshold.
type benchWorld struct {
	fow  *FanoutOnWrite
	for_ *FanoutOnRead
	hy   *Hybrid
}

const (
	celebrityFollowers = 1000
	postsPerAuthor     = 40
	benchLimit         = 50
)

var (
	worldMu sync.Mutex
	worlds  = map[int]*benchWorld{}
)

func getWorld(ordinary int) *benchWorld {
	worldMu.Lock()
	defer worldMu.Unlock()
	if w, ok := worlds[ordinary]; ok {
		return w
	}
	w := newWorld()
	g, posts := w.g, w.posts
	_ = posts

	authors := make([]string, 0, ordinary+3)
	for i := 0; i < ordinary; i++ {
		a := "a" + strconv.Itoa(i)
		g.Follow("reader", a)
		authors = append(authors, a)
	}
	for c := 0; c < 3; c++ {
		name := "c" + strconv.Itoa(c)
		g.Follow("reader", name)
		for i := 0; i < celebrityFollowers-1; i++ {
			g.Follow("x"+strconv.Itoa(i), name)
		}
		authors = append(authors, name)
	}

	bw := &benchWorld{
		fow:  NewFanoutOnWrite(g, 800),
		for_: NewFanoutOnRead(g, w.posts),
		hy:   NewHybrid(g, w.posts, 800, celebrityFollowers),
	}
	for round := 0; round < postsPerAuthor; round++ {
		for _, a := range authors {
			w.publish(a, bw.fow, bw.for_, bw.hy)
		}
	}
	worlds[ordinary] = bw
	return bw
}

// BenchmarkGetTimeline measures one timeline read as the number of people
// the reader follows grows. The reader always follows 3 celebrities on top
// of the F ordinary authors.
func BenchmarkGetTimeline(b *testing.B) {
	for _, f := range []int{10, 100, 1000, 5000} {
		w := getWorld(f)
		for _, s := range []struct {
			name string
			s    Strategy
		}{
			{"fan-out-on-write", w.fow},
			{"fan-out-on-read", w.for_},
			{"hybrid", w.hy},
		} {
			b.Run(fmt.Sprintf("followees=%d/%s", f+3, s.name), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					s.s.GetTimeline("reader", benchLimit)
				}
			})
		}
	}
}

// BenchmarkOnPost measures the write side for an ordinary author with one
// follower, so every strategy is doing its normal-case amount of work.
func BenchmarkOnPost(b *testing.B) {
	for _, mk := range []struct {
		name string
		make func(w *world) Strategy
	}{
		{"fan-out-on-write", func(w *world) Strategy { return NewFanoutOnWrite(w.g, 800) }},
		{"fan-out-on-read", func(w *world) Strategy { return NewFanoutOnRead(w.g, w.posts) }},
		{"hybrid", func(w *world) Strategy { return NewHybrid(w.g, w.posts, 800, 1000) }},
	} {
		b.Run(mk.name, func(b *testing.B) {
			w := newWorld()
			w.g.Follow("fan", "author")
			s := mk.make(w)
			// A small fixed pool, cycled. Sizing this by b.N is a trap: for
			// a near-free operation like fan-out on read's no-op OnPost the
			// framework raises b.N past a billion, and a pool that large
			// exhausts memory before measuring anything. Built directly
			// rather than through post.Store, which prepends to a per-author
			// slice and isn't what this measures.
			posts := make([]*post.Post, 1024)
			for i := range posts {
				posts[i] = &post.Post{ID: strconv.Itoa(i), Seq: int64(i), Author: "author"}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.OnPost(posts[i%len(posts)])
			}
		})
	}
}

var _ = graph.New
