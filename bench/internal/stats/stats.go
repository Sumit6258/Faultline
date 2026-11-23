// Package stats has the small amount of statistics benchmark comparison needs:
// a robust summary of repeated runs, and a test for whether two sets of runs
// really differ.
package stats

import (
	"math"
	"sort"
)

// Summary describes repeated measurements of one thing.
type Summary struct {
	N        int
	Median   float64
	Min, Max float64
	Mean     float64
	// Spread is how far apart the runs were, as a fraction of the median:
	// (max - min) / median. It is the number to look at before trusting the
	// median. A benchmark whose runs vary by 30% cannot show a 5% change.
	Spread float64
}

func Summarize(xs []float64) Summary {
	if len(xs) == 0 {
		return Summary{}
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	var sum float64
	for _, x := range s {
		sum += x
	}
	sm := Summary{N: n, Min: s[0], Max: s[n-1], Mean: sum / float64(n)}
	if n%2 == 1 {
		sm.Median = s[n/2]
	} else {
		sm.Median = (s[n/2-1] + s[n/2]) / 2
	}
	if sm.Median != 0 {
		sm.Spread = (sm.Max - sm.Min) / math.Abs(sm.Median)
	}
	return sm
}

// MaxExact is the largest sample size for which the exact distribution is
// used. Beyond it the counts stop being exactly representable.
const MaxExact = 25

// MannWhitney returns the two sided p-value of the Mann-Whitney U test: the
// probability of runs that differ this much if a and b really came from the
// same distribution. It compares ranks, not values, so a single wild run
// cannot dominate it the way it dominates a comparison of means, and it
// assumes nothing about the shape of the timing distribution, which for
// benchmarks is skewed and has a long tail.
//
// It is exact when there are no ties and both samples are at most MaxExact.
// Otherwise it uses the normal approximation with a tie correction and a
// continuity correction, as scipy's mannwhitneyu does. exact reports which.
func MannWhitney(a, b []float64) (p float64, exact bool) {
	n1, n2 := len(a), len(b)
	if n1 == 0 || n2 == 0 {
		return 1, false
	}
	type item struct {
		v     float64
		first bool
	}
	all := make([]item, 0, n1+n2)
	for _, v := range a {
		all = append(all, item{v, true})
	}
	for _, v := range b {
		all = append(all, item{v, false})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v < all[j].v })

	// average ranks, and the tie groups for the variance correction
	n := len(all)
	var r1, tieTerm float64
	hasTies := false
	for i := 0; i < n; {
		j := i
		for j < n && all[j].v == all[i].v {
			j++
		}
		avg := float64(i+j+1) / 2 // ranks are 1 based: i+1 .. j
		for k := i; k < j; k++ {
			if all[k].first {
				r1 += avg
			}
		}
		if t := float64(j - i); t > 1 {
			hasTies = true
			tieTerm += t*t*t - t
		}
		i = j
	}
	u1 := r1 - float64(n1*(n1+1))/2
	u2 := float64(n1*n2) - u1
	u := math.Min(u1, u2)

	if !hasTies && n1 <= MaxExact && n2 <= MaxExact {
		return exactP(n1, n2, u), true
	}

	mean := float64(n1*n2) / 2
	variance := float64(n1*n2) / 12 * (float64(n+1) - tieTerm/float64(n*(n-1)))
	if variance <= 0 {
		return 1, false // every value is the same
	}
	z := (math.Abs(u-mean) - 0.5) / math.Sqrt(variance)
	if z < 0 {
		z = 0
	}
	return math.Erfc(z / math.Sqrt2), false
}

// exactP is P(U <= u) doubled, from the exact distribution of U for n1 and n2
// observations with no ties. ways[i][j][k] counts the arrangements of i
// values from one sample and j from the other that give U = k.
func exactP(n1, n2 int, u float64) float64 {
	ways := make([][][]float64, n1+1)
	for i := range ways {
		ways[i] = make([][]float64, n2+1)
		for j := range ways[i] {
			ways[i][j] = make([]float64, i*j+1)
			switch {
			case i == 0 || j == 0:
				ways[i][j][0] = 1
			default:
				for k := range ways[i][j] {
					// the largest value belongs to the first sample, and beats all j of the others,
					// or it belongs to the second sample and beats none of the first's
					if k >= j && k-j < len(ways[i-1][j]) {
						ways[i][j][k] += ways[i-1][j][k-j]
					}
					if k < len(ways[i][j-1]) {
						ways[i][j][k] += ways[i][j-1][k]
					}
				}
			}
		}
	}
	dist := ways[n1][n2]
	var total, tail float64
	for k, c := range dist {
		total += c
		if float64(k) <= u {
			tail += c
		}
	}
	return math.Min(1, 2*tail/total)
}
