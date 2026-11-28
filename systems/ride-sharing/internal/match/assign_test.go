package match

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

// bestBySearch tries every way to pair rows with columns, which is only possible for tiny matrices and is
// obviously correct. It prefers more rows matched, then lower cost.
func bestBySearch(cost [][]float64) (matched int, total float64) {
	rows := len(cost)
	cols := 0
	if rows > 0 {
		cols = len(cost[0])
	}
	bestM, bestT := -1, math.Inf(1)
	used := make([]bool, cols)
	var rec func(i, m int, t float64)
	rec = func(i, m int, t float64) {
		if i == rows {
			if m > bestM || (m == bestM && t < bestT) {
				bestM, bestT = m, t
			}
			return
		}
		rec(i+1, m, t) // leave this row unmatched
		for j := 0; j < cols; j++ {
			if !used[j] && !math.IsInf(cost[i][j], 1) {
				used[j] = true
				rec(i+1, m+1, t+cost[i][j])
				used[j] = false
			}
		}
	}
	rec(0, 0, 0)
	return bestM, bestT
}

func randCost(r *rand.Rand, rows, cols int, forbidden float64) [][]float64 {
	c := make([][]float64, rows)
	for i := range c {
		c[i] = make([]float64, cols)
		for j := range c[i] {
			if r.Float64() < forbidden {
				c[i][j] = Forbidden
			} else {
				c[i][j] = float64(r.Intn(1000)) / 10
			}
		}
	}
	return c
}

func validAssignment(t *testing.T, cost [][]float64, assigned []int) {
	t.Helper()
	if len(assigned) != len(cost) {
		t.Fatalf("%d assignments for %d rows", len(assigned), len(cost))
	}
	seen := map[int]bool{}
	for i, j := range assigned {
		if j < 0 {
			continue
		}
		if j >= len(cost[i]) || seen[j] || math.IsInf(cost[i][j], 1) {
			t.Fatalf("row %d got column %d, which is out of range, taken twice, or forbidden", i, j)
		}
		seen[j] = true
	}
}

func TestOptimalMatchesTheBestOfAllPossibleAssignments(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	for run := 0; run < 1500; run++ {
		rows, cols := 1+r.Intn(6), 1+r.Intn(6)
		cost := randCost(r, rows, cols, []float64{0, 0.3, 0.7}[r.Intn(3)])
		got := Optimal(cost)
		validAssignment(t, cost, got)
		gotTotal, gotMatched := Total(cost, got)
		wantMatched, wantTotal := bestBySearch(cost)
		if gotMatched != wantMatched || math.Abs(gotTotal-wantTotal) > 1e-9 {
			t.Fatalf("%dx%d %v\nOptimal matched %d for %.1f, the best possible is %d for %.1f (%v)", rows, cols, cost, gotMatched, gotTotal, wantMatched, wantTotal, got)
		}
	}
}

func TestOptimalIsNeverWorseThanGreedy(t *testing.T) {
	r := rand.New(rand.NewSource(12))
	strictly := 0
	for run := 0; run < 800; run++ {
		rows, cols := 1+r.Intn(14), 1+r.Intn(14)
		cost := randCost(r, rows, cols, []float64{0, 0.4}[r.Intn(2)])
		og, gg := Optimal(cost), Greedy(cost)
		validAssignment(t, cost, og)
		validAssignment(t, cost, gg)
		ot, om := Total(cost, og)
		gt, gm := Total(cost, gg)
		if om < gm || (om == gm && ot > gt+1e-9) {
			t.Fatalf("greedy matched %d for %.1f, optimal %d for %.1f", gm, gt, om, ot)
		}
		if om > gm || ot < gt-1e-9 {
			strictly++
		}
	}
	if strictly < 100 {
		t.Errorf("optimal should beat greedy in many random cases, did in only %d of 800", strictly)
	}
}

// The classic case. Rider 0 is a little nearer driver 0 than driver 1, but driver 0 is the only one close
// enough to rider 1. Taking the nearest for rider 0 first leaves rider 1 with the far driver.
func TestGreedyTakesTheDriverTheNextRiderNeeds(t *testing.T) {
	cost := [][]float64{{10, 12}, {11, 300}}
	g, o := Greedy(cost), Optimal(cost)
	if gt, _ := Total(cost, g); gt != 310 {
		t.Errorf("greedy %v totals %.0f, expected the poor outcome of 310", g, gt)
	}
	if ot, _ := Total(cost, o); ot != 23 {
		t.Errorf("optimal %v totals %.0f, want 23", o, ot)
	}
}

func TestGreedyGivesEachRowTheCheapestColumnStillFree(t *testing.T) {
	cost := [][]float64{
		{9, 2, 7, Forbidden},
		{9, 2, 8, 1},
		{3, 4, 5, Forbidden},
	}
	// Row 0 takes column 1 (cost 2). Row 1 would like column 1 too, but it is gone, so it takes column 3 (cost 1).
	// Row 2 takes the cheapest left, column 0 (cost 3).
	got := Greedy(cost)
	if got[0] != 1 || got[1] != 3 || got[2] != 0 {
		t.Errorf("%v", got)
	}
}

func TestEdgeCases(t *testing.T) {
	if got := Optimal(nil); len(got) != 0 {
		t.Errorf("%v", got)
	}
	if got := Optimal([][]float64{{}, {}}); got[0] != -1 || got[1] != -1 {
		t.Errorf("no columns: %v", got)
	}
	all := [][]float64{{Forbidden, Forbidden}, {Forbidden, Forbidden}}
	if got := Optimal(all); got[0] != -1 || got[1] != -1 {
		t.Errorf("nothing allowed: %v", got)
	}
	if got := Greedy(all); got[0] != -1 || got[1] != -1 {
		t.Errorf("nothing allowed, greedy: %v", got)
	}
	// More riders than drivers: the two drivers go to the two riders they are cheapest for.
	cost := [][]float64{{5}, {1}, {9}}
	if got := Optimal(cost); got[0] != -1 || got[1] != 0 || got[2] != -1 {
		t.Errorf("one driver, three riders: %v", got)
	}
	// More drivers than riders.
	if got := Optimal([][]float64{{9, 2, 7}}); got[0] != 1 {
		t.Errorf("%v", got)
	}
}

// Leaving rider 0 unmatched would be cheaper (1 against 1001), but it would leave a rider without a ride
// when both can have one. Matching more people comes before saving cost.
func TestMatchingMorePeopleComesBeforeSavingCost(t *testing.T) {
	cost := [][]float64{
		{1, Forbidden},
		{1, 1000},
	}
	got := Optimal(cost)
	if total, matched := Total(cost, got); matched != 2 || total != 1001 || got[0] != 0 || got[1] != 1 {
		t.Errorf("matched %d for %.0f: %v", matched, total, got)
	}
}

func TestAHundredAndFiftyByAHundredAndFiftyIsFast(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	cost := randCost(r, 150, 150, 0.5)
	got := Optimal(cost)
	validAssignment(t, cost, got)
}

func TestOptimalDoesNotModifyItsInput(t *testing.T) {
	cost := [][]float64{{3, 1}, {2, 5}}
	Optimal(cost)
	Greedy(cost)
	if cost[0][0] != 3 || cost[0][1] != 1 || cost[1][0] != 2 || cost[1][1] != 5 {
		t.Errorf("%v", cost)
	}
}

func BenchmarkOptimal(b *testing.B) {
	for _, n := range []int{10, 50, 200} {
		r := rand.New(rand.NewSource(1))
		cost := randCost(r, n, n, 0.5)
		b.Run(sizeName(n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				Optimal(cost)
			}
		})
	}
}

func BenchmarkGreedy(b *testing.B) {
	for _, n := range []int{10, 50, 200} {
		r := rand.New(rand.NewSource(1))
		cost := randCost(r, n, n, 0.5)
		b.Run(sizeName(n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				Greedy(cost)
			}
		})
	}
}

func sizeName(n int) string {
	switch n {
	case 10:
		return "riders=10"
	case 50:
		return "riders=50"
	}
	return "riders=200"
}

// The solver works on the smaller side, so very lopsided problems, a few riders and many drivers or the other
// way round, take different paths through it. Checked against trying every pairing, as the square ones are.
func TestLopsidedProblemsMatchTheBestOfAllPossibleAssignments(t *testing.T) {
	r := rand.New(rand.NewSource(21))
	for run := 0; run < 1200; run++ {
		var rows, cols int
		if run%2 == 0 {
			rows, cols = 1+r.Intn(4), 6+r.Intn(7) // a few riders, many drivers
		} else {
			rows, cols = 6+r.Intn(3), 1+r.Intn(4) // many riders, a few drivers
		}
		cost := randCost(r, rows, cols, []float64{0, 0.3, 0.6}[r.Intn(3)])
		got := Optimal(cost)
		validAssignment(t, cost, got)
		gotTotal, gotMatched := Total(cost, got)
		wantMatched, wantTotal := bestBySearch(cost)
		if gotMatched != wantMatched || math.Abs(gotTotal-wantTotal) > 1e-9 {
			t.Fatalf("%dx%d %v\nmatched %d for %.1f, the best possible is %d for %.1f", rows, cols, cost, gotMatched, gotTotal, wantMatched, wantTotal)
		}
	}
}

func BenchmarkOptimalLopsided(b *testing.B) {
	for _, c := range []struct{ riders, drivers int }{{10, 80}, {50, 400}, {150, 1200}} {
		r := rand.New(rand.NewSource(1))
		cost := randCost(r, c.riders, c.drivers, 0.9) // each rider can only reach about a tenth of the drivers
		b.Run(fmt.Sprintf("riders=%d/drivers=%d", c.riders, c.drivers), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				Optimal(cost)
			}
		})
	}
}
