package report

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Sumit6258/Faultline/bench/internal/model"
	"github.com/Sumit6258/Faultline/bench/internal/stats"
)

type CompareOptions struct {
	// Threshold is the smallest change worth reporting, as a fraction. A change smaller than this
	// is not reported however significant, because it is not worth acting on.
	Threshold float64
	// Alpha is the p-value below which a difference is called real.
	Alpha float64
	// Force compares timings even when the two results came from different machines.
	Force bool
}

const (
	Slower  = "slower"
	Faster  = "faster"
	Same    = "no change"
	TooFew  = "too few runs"
	NotDone = "not compared"
)

type Row struct {
	Key, Name          string
	Old, New           stats.Summary
	Delta              float64 // (new - old) / old
	P                  float64
	Verdict            string
	OldAllocs          float64
	NewAllocs          float64
	HasAllocs          bool
	OldBytes, NewBytes float64
}

type Comparison struct {
	Rows             []Row
	Added, Removed   []string
	TimingComparable bool
	Reason           string
	Forced           bool
	Opts             CompareOptions
}

// minP is the smallest p-value that two samples of these sizes could ever
// produce, which is reached when every run of one is slower than every run of
// the other. If even that is above alpha, no result with this few runs can be called significant.
func minP(n1, n2 int) float64 {
	c := 1.0 // C(n1+n2, n1)
	for i := 1; i <= n1; i++ {
		c = c * float64(n2+i) / float64(i)
	}
	return math.Min(1, 2/c)
}

func Compare(old, cur model.File, o CompareOptions) Comparison {
	c := Comparison{Opts: o, Forced: o.Force}
	c.TimingComparable, c.Reason = model.Comparable(old.Env, cur.Env)

	oldBy := map[string]model.Series{}
	for _, s := range old.Series {
		oldBy[s.Key()] = s
	}
	curBy := map[string]model.Series{}
	for _, s := range cur.Series {
		curBy[s.Key()] = s
	}

	for _, s := range cur.Series {
		os, ok := oldBy[s.Key()]
		if !ok {
			c.Added = append(c.Added, s.Key())
			continue
		}
		r := Row{Key: s.Key(), Name: s.Module + ": " + DisplayName(s)}
		ot, ook := summary(os, "ns/op")
		nt, nok := summary(s, "ns/op")
		r.Old, r.New = ot, nt
		if oa, ok := summary(os, "allocs/op"); ok {
			if na, ok := summary(s, "allocs/op"); ok {
				r.HasAllocs, r.OldAllocs, r.NewAllocs = true, oa.Median, na.Median
			}
		}
		if ob, ok := summary(os, "B/op"); ok {
			if nb, ok := summary(s, "B/op"); ok {
				r.OldBytes, r.NewBytes = ob.Median, nb.Median
			}
		}
		switch {
		case !ook || !nok:
			r.Verdict = NotDone
		case !c.TimingComparable && !o.Force:
			r.Verdict = NotDone
		default:
			if ot.Median != 0 {
				r.Delta = (nt.Median - ot.Median) / ot.Median
			}
			r.P, _ = stats.MannWhitney(os.Samples["ns/op"], s.Samples["ns/op"])
			switch {
			case minP(ot.N, nt.N) > o.Alpha:
				r.Verdict = TooFew
			case r.P < o.Alpha && math.Abs(r.Delta) >= o.Threshold:
				if r.Delta > 0 {
					r.Verdict = Slower
				} else {
					r.Verdict = Faster
				}
			default:
				r.Verdict = Same
			}
		}
		c.Rows = append(c.Rows, r)
	}
	for _, s := range old.Series {
		if _, ok := curBy[s.Key()]; !ok {
			c.Removed = append(c.Removed, s.Key())
		}
	}
	sort.Strings(c.Added)
	sort.Strings(c.Removed)
	return c
}

func (c Comparison) count(v string) (n int) {
	for _, r := range c.Rows {
		if r.Verdict == v {
			n++
		}
	}
	return
}

func (c Comparison) AllocsUp() (n int) {
	for _, r := range c.Rows {
		if r.HasAllocs && r.NewAllocs > r.OldAllocs {
			n++
		}
	}
	return
}

func (c Comparison) AllocsDown() (n int) {
	for _, r := range c.Rows {
		if r.HasAllocs && r.NewAllocs < r.OldAllocs {
			n++
		}
	}
	return
}

// timed counts the benchmarks whose timings were actually tested.
func (c Comparison) timed() (n int) {
	for _, r := range c.Rows {
		if r.Verdict != NotDone && r.Verdict != TooFew {
			n++
		}
	}
	return
}

// ExpectedByChance is how many of the tested benchmarks would be flagged as changed even if nothing
// had changed, simply because a p-value below alpha happens by luck alpha of the time. It is a
// ceiling on how much to read into one flagged benchmark in a long list.
func (c Comparison) ExpectedByChance() float64 { return float64(c.timed()) * c.Opts.Alpha }

// HasRegression is true if anything got significantly slower or allocates more.
func (c Comparison) HasRegression() bool { return c.count(Slower) > 0 || c.AllocsUp() > 0 }

func pct(v float64) string { return fmt.Sprintf("%+.1f%%", v*100) }

func (c Comparison) Markdown() string {
	var b strings.Builder
	b.WriteString("# Benchmark comparison\n\n")
	if !c.TimingComparable {
		if c.Forced {
			fmt.Fprintf(&b, "**Warning: these results come from different environments (%s), and timings were compared anyway because --force was given. Treat every timing verdict with suspicion.**\n\n", c.Reason)
		} else {
			fmt.Fprintf(&b, "**Timings were not compared. The two results come from different environments (%s), and a time on one machine says nothing about a time on another.** Allocation counts, which do not depend on the machine, are still compared.\n\n", c.Reason)
		}
	}
	fmt.Fprintf(&b, "%d benchmarks compared: %d slower, %d faster, %d no significant change, %d with too few runs to tell. Allocations went up in %d and down in %d.\n\n",
		len(c.Rows), c.count(Slower), c.count(Faster), c.count(Same), c.count(TooFew), c.AllocsUp(), c.AllocsDown())
	fmt.Fprintf(&b, "A change is called real when its p-value (Mann-Whitney U test) is below %.2g and the median moved by at least %.0f%%.\n\n", c.Opts.Alpha, c.Opts.Threshold*100)
	if chance := c.ExpectedByChance(); chance >= 1 {
		fmt.Fprintf(&b, "Comparing %d benchmarks at that level, about %.0f would be flagged by chance even if nothing had changed. A single flagged benchmark among many is weak evidence. A change that shows up in the benchmarks that cover the code you touched is strong evidence.\n\n", c.timed(), chance)
	}

	b.WriteString("| Benchmark | Old | New | Change | p | Verdict | allocs/op |\n|---|---|---|---|---|---|---|\n")
	for _, r := range c.Rows {
		oldT, newT, change, p := "n/a", "n/a", "n/a", "n/a"
		if r.Verdict != NotDone {
			oldT, newT, change = FormatTime(r.Old.Median), FormatTime(r.New.Median), pct(r.Delta)
			p = fmt.Sprintf("%.3f", r.P)
		}
		allocs := "n/a"
		if r.HasAllocs {
			allocs = intStr(r.NewAllocs)
			if r.NewAllocs != r.OldAllocs {
				allocs = fmt.Sprintf("%s to %s", intStr(r.OldAllocs), intStr(r.NewAllocs))
			}
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n", r.Name, oldT, newT, change, p, r.Verdict, allocs)
	}
	b.WriteString("\n")
	if len(c.Added) > 0 {
		fmt.Fprintf(&b, "Only in the new results: %s\n\n", strings.Join(c.Added, ", "))
	}
	if len(c.Removed) > 0 {
		fmt.Fprintf(&b, "Only in the old results: %s\n\n", strings.Join(c.Removed, ", "))
	}
	return b.String()
}
