// The celebrity problem, measured: one account with N followers makes one
// post. How much work does each timeline strategy do at post time, and what
// does a follower pay to read it afterward?
//
// Everything here runs in one process on one core, the same limits as the
// rest of this repository's measurements. Nothing is estimated except where
// the output says "extrapolated", and those lines are arithmetic on a
// measured rate, not a measurement.
package main

import (
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"time"

	"github.com/Sumit6258/Faultline/systems/social-feed/internal/graph"
	"github.com/Sumit6258/Faultline/systems/social-feed/internal/post"
	"github.com/Sumit6258/Faultline/systems/social-feed/internal/timeline"
)

const (
	celebrityThreshold = 10_000
	maxInbox           = 800
	repeats            = 3
)

type strat struct {
	name string
	make func(g *graph.Graph, p *post.Store) timeline.Strategy
}

var strategies = []strat{
	{"fan-out on write", func(g *graph.Graph, p *post.Store) timeline.Strategy { return timeline.NewFanoutOnWrite(g, maxInbox) }},
	{"fan-out on read", func(g *graph.Graph, p *post.Store) timeline.Strategy { return timeline.NewFanoutOnRead(g, p) }},
	{"hybrid (threshold 10k)", func(g *graph.Graph, p *post.Store) timeline.Strategy {
		return timeline.NewHybrid(g, p, maxInbox, celebrityThreshold)
	}},
}

func median(ds []time.Duration) time.Duration {
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}

func main() {
	fmt.Printf("one post by an account with N followers, median of %d runs, GOMAXPROCS=%d\n\n", repeats, runtime.GOMAXPROCS(0))
	fmt.Printf("%-10s %-24s %14s %14s %16s\n", "followers", "strategy", "post latency", "inbox writes", "ns per follower")

	minPer, maxPer := 0.0, 0.0
	for _, n := range []int{1_000, 10_000, 100_000, 1_000_000} {
		g := graph.New()
		posts := post.NewStore()
		reader := "f0"
		for i := 0; i < n; i++ {
			g.Follow("f"+strconv.Itoa(i), "star")
		}
		p := posts.Create("star", "hello")

		for _, st := range strategies {
			var times []time.Duration
			var writes int64
			for r := 0; r < repeats; r++ {
				s := st.make(g, posts)
				runtime.GC()
				t0 := time.Now()
				s.OnPost(p)
				times = append(times, time.Since(t0))
				writes = s.InboxWrites()
			}
			d := median(times)
			fmt.Printf("%-10d %-24s %14s %14d %16.0f\n", n, st.name, d.Round(time.Microsecond), writes, float64(d.Nanoseconds())/float64(n))
			if st.name == "fan-out on write" {
				per := float64(d.Nanoseconds()) / float64(n)
				if minPer == 0 || per < minPer {
					minPer = per
				}
				if per > maxPer {
					maxPer = per
				}
			}
		}
		fmt.Println()
		runtime.GC()

		if n == 100_000 {
			fmt.Printf("read cost for one follower afterward (reader follows only the star), N=%d, mean of 2000 reads:\n", n)
			for _, st := range strategies {
				s := st.make(g, posts)
				s.OnPost(p)
				const reads = 2000
				t0 := time.Now()
				for i := 0; i < reads; i++ {
					s.GetTimeline(reader, 50)
				}
				fmt.Printf("  %-24s %10s per read\n", st.name, (time.Since(t0) / reads).Round(10*time.Nanosecond))
			}
			fmt.Println()
		}
	}

	fmt.Printf("fan-out on write cost between %.0f and %.0f ns per follower across the sizes measured, so no single\n", minPer, maxPer)
	fmt.Println("rate is quoted for larger accounts. What does not vary is the count, exactly one inbox write per follower:")
	for _, n := range []int{10_000_000, 100_000_000} {
		fmt.Printf("  one post by an account with %d followers = %d inbox writes\n", n, n)
	}
}
