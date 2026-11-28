package geo

import (
	"fmt"
	"math"
	"math/rand"
	"sync"
	"testing"
)

var areas = []struct {
	name string
	b    Bounds
}{
	{"equatorial", Bounds{-0.10, 0.10, 36.70, 36.95}},
	{"mid latitude", Bounds{12.85, 13.10, 77.45, 77.75}},
	{"high latitude", Bounds{59.80, 60.10, 24.70, 25.20}},
	{"very high latitude", Bounds{64.00, 64.30, -22.20, -21.60}},
}

func randPoint(r *rand.Rand, b Bounds) Point {
	return Point{b.MinLat + r.Float64()*(b.MaxLat-b.MinLat), b.MinLon + r.Float64()*(b.MaxLon-b.MinLon)}
}

func same(t *testing.T, ctx string, got, want []Result) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %d results, want %d\n got %v\nwant %v", ctx, len(got), len(want), got, want)
	}
	for i := range got {
		if got[i].ID != want[i].ID || got[i].Meters != want[i].Meters {
			t.Fatalf("%s: result %d is %s at %.3f m, want %s at %.3f m", ctx, i, got[i].ID, got[i].Meters, want[i].ID, want[i].Meters)
		}
	}
}

func TestDistance(t *testing.T) {
	// One degree of latitude is about 111.2 km everywhere.
	if d := Distance(Point{10, 20}, Point{11, 20}); math.Abs(d-MetersPerDegreeLat) > 1 {
		t.Errorf("one degree of latitude is %.1f m, want %.1f", d, MetersPerDegreeLat)
	}
	// A degree of longitude shrinks with the cosine of the latitude.
	if d, want := Distance(Point{60, 20}, Point{60, 21}), MetersPerDegreeLat*0.5; math.Abs(d-want) > 300 {
		t.Errorf("one degree of longitude at 60 north is %.1f m, want about %.1f", d, want)
	}
	if Distance(Point{1, 2}, Point{1, 2}) != 0 {
		t.Error("a point is zero metres from itself")
	}
	a, b := Point{12.9, 77.5}, Point{13.0, 77.7}
	if Distance(a, b) != Distance(b, a) {
		t.Error("distance is not symmetric")
	}
}

func TestBoundsAreValidated(t *testing.T) {
	for name, b := range map[string]Bounds{
		"empty":      {10, 10, 20, 21},
		"inverted":   {11, 10, 20, 21},
		"at a pole":  {80, 89, 0, 10},
		"nonsense":   {-100, 10, 0, 10},
		"not a real": {math.NaN(), 10, 0, 10},
	} {
		if _, err := NewGrid(b, 500); err == nil {
			t.Errorf("%s bounds should be refused", name)
		}
	}
	if _, err := NewGrid(areas[1].b, 0); err == nil {
		t.Error("a zero cell size should be refused")
	}
}

// The grid is only worth having if it gives the same answer as the version that
// looks at everything. This compares them on random cities, at four latitudes,
// at three cell sizes, with moves, removals, filters, and queries from inside
// and outside the service area.
func TestGridAgreesWithBruteForce(t *testing.T) {
	for _, area := range areas {
		for _, cell := range []float64{150, 500, 2000} {
			t.Run(fmt.Sprintf("%s/cell=%.0fm", area.name, cell), func(t *testing.T) {
				for seed := int64(1); seed <= 12; seed++ {
					r := rand.New(rand.NewSource(seed))
					grid, err := NewGrid(area.b, cell)
					if err != nil {
						t.Fatal(err)
					}
					brute := NewBruteForce()
					n := r.Intn(500)
					cluster := randPoint(r, area.b)
					for i := 0; i < n; i++ {
						p := randPoint(r, area.b)
						if i%2 == 0 { // half of them crowded together, so some cells hold many
							p = Point{cluster.Lat + (r.Float64()-0.5)*0.01, cluster.Lon + (r.Float64()-0.5)*0.01}
							p.Lat = math.Min(math.Max(p.Lat, area.b.MinLat), area.b.MaxLat)
							p.Lon = math.Min(math.Max(p.Lon, area.b.MinLon), area.b.MaxLon)
						}
						id := fmt.Sprintf("d%d", i)
						grid.Upsert(id, p)
						brute.Upsert(id, p)
					}
					for i := 0; i < n/3; i++ { // moves
						id := fmt.Sprintf("d%d", r.Intn(n+1))
						p := randPoint(r, area.b)
						grid.Upsert(id, p)
						brute.Upsert(id, p)
					}
					for i := 0; i < n/10; i++ { // removals
						id := fmt.Sprintf("d%d", r.Intn(n+1))
						grid.Remove(id)
						brute.Remove(id)
					}
					if grid.Len() != brute.Len() {
						t.Fatalf("len %d vs %d", grid.Len(), brute.Len())
					}
					for q := 0; q < 120; q++ {
						pt := randPoint(r, area.b)
						switch q % 6 {
						case 1: // exactly where a driver is
							if p, ok := brute.Get(fmt.Sprintf("d%d", r.Intn(n+1))); ok {
								pt = p
							}
						case 2: // outside the service area, on any side or in any corner
							dLat, dLon := 0.0, 0.0
							switch r.Intn(8) {
							case 0:
								dLat = area.b.MaxLat - pt.Lat + r.Float64()*0.2
							case 1:
								dLat = area.b.MinLat - pt.Lat - r.Float64()*0.2
							case 2:
								dLon = area.b.MaxLon - pt.Lon + r.Float64()*0.2
							case 3:
								dLon = area.b.MinLon - pt.Lon - r.Float64()*0.2
							case 4:
								dLat, dLon = area.b.MaxLat-pt.Lat+r.Float64()*0.2, area.b.MaxLon-pt.Lon+r.Float64()*0.2
							case 5:
								dLat, dLon = area.b.MinLat-pt.Lat-r.Float64()*0.2, area.b.MinLon-pt.Lon-r.Float64()*0.2
							case 6:
								dLat, dLon = area.b.MaxLat-pt.Lat+r.Float64()*0.2, area.b.MinLon-pt.Lon-r.Float64()*0.2
							default:
								dLat, dLon = area.b.MinLat-pt.Lat-r.Float64()*0.2, area.b.MaxLon-pt.Lon+r.Float64()*0.2
							}
							pt = Point{pt.Lat + dLat, pt.Lon + dLon}
						}
						k := []int{1, 3, 8, 50}[r.Intn(4)]
						max := []float64{0, 300, 1500, 8000}[r.Intn(4)]
						var accept func(string) bool
						if r.Intn(2) == 0 {
							accept = func(id string) bool { return len(id)%2 == 0 }
						}
						ctx := fmt.Sprintf("seed %d query %d at %v k=%d max=%.0f", seed, q, pt, k, max)
						same(t, ctx, grid.Nearest(pt, k, max, accept), brute.Nearest(pt, k, max, accept))
					}
				}
			})
		}
	}
}

func TestADriverAtTheQueryPointIsFoundFirstAtZeroDistance(t *testing.T) {
	g, _ := NewGrid(areas[1].b, 500)
	p := Point{12.97, 77.59}
	g.Upsert("here", p)
	g.Upsert("near", Point{12.9701, 77.59})
	got := g.Nearest(p, 2, 0, nil)
	if len(got) != 2 || got[0].ID != "here" || got[0].Meters != 0 {
		t.Errorf("%+v", got)
	}
}

// The nearest driver can be in a diagonal neighbour of the rider's cell while a
// farther one sits in the rider's own cell. Stopping after the rider's own cell
// would return the wrong driver.
func TestADiagonalNeighbourCanBeNearerThanSomeoneInTheSameCell(t *testing.T) {
	g, _ := NewGrid(areas[1].b, 500)
	cg := g.CellGrid()
	corner := Point{cg.Bounds.MinLat + 10*cg.LatStep, cg.Bounds.MinLon + 10*cg.LonStep}
	q := Point{corner.Lat - 0.00001, corner.Lon - 0.00001}                      // inside cell 9,9, at its corner
	diag := Point{corner.Lat + 0.00001, corner.Lon + 0.00001}                   // inside cell 10,10, about 3 m away
	sameCell := Point{corner.Lat - 0.9*cg.LatStep, corner.Lon - 0.9*cg.LonStep} // inside cell 9,9, hundreds of metres away
	if x, y := cg.Cell(q); x != 9 || y != 9 {
		t.Fatalf("setup: q is in cell %d,%d", x, y)
	}
	if x, y := cg.Cell(diag); x != 10 || y != 10 {
		t.Fatalf("setup: diag is in cell %d,%d", x, y)
	}
	g.Upsert("far-same-cell", sameCell)
	g.Upsert("close-diagonal", diag)
	if got := g.Nearest(q, 1, 0, nil); len(got) != 1 || got[0].ID != "close-diagonal" {
		t.Errorf("%+v", got)
	}
}

func TestAFarAwayDriverIsStillFoundWhenNothingIsNearer(t *testing.T) {
	g, _ := NewGrid(areas[1].b, 200)
	g.Upsert("far", Point{13.09, 77.74})
	got := g.Nearest(Point{12.86, 77.46}, 1, 0, nil)
	if len(got) != 1 || got[0].ID != "far" {
		t.Errorf("%+v", got)
	}
	if got := g.Nearest(Point{12.86, 77.46}, 1, 1000, nil); len(got) != 0 {
		t.Errorf("the radius should exclude it: %+v", got)
	}
}

func TestEdgesOfTheQueryAreHonoured(t *testing.T) {
	g, _ := NewGrid(areas[1].b, 500)
	for i := 0; i < 10; i++ {
		g.Upsert(fmt.Sprintf("d%d", i), Point{12.97 + float64(i)*0.001, 77.59})
	}
	q := Point{12.97, 77.59}
	if got := g.Nearest(q, 0, 0, nil); got != nil {
		t.Errorf("k=0 returns nothing: %v", got)
	}
	if got := g.Nearest(q, 100, 0, nil); len(got) != 10 {
		t.Errorf("k larger than the population returns all of it: %d", len(got))
	}
	if got := g.Nearest(q, 100, 0, func(id string) bool { return id == "d3" }); len(got) != 1 || got[0].ID != "d3" {
		t.Errorf("accept filter: %v", got)
	}
	if got := NewBruteForce().Nearest(q, 3, 0, nil); len(got) != 0 {
		t.Errorf("an empty index: %v", got)
	}
}

func TestMovingBetweenCellsLeavesNoCopyBehind(t *testing.T) {
	g, _ := NewGrid(areas[1].b, 500)
	a, b := Point{12.90, 77.50}, Point{13.05, 77.70}
	g.Upsert("d", a)
	g.Upsert("d", b)
	if g.Len() != 1 {
		t.Errorf("len %d", g.Len())
	}
	if got := g.Nearest(a, 5, 1000, nil); len(got) != 0 {
		t.Errorf("the old position still answers: %v", got)
	}
	if got := g.Nearest(b, 5, 1000, nil); len(got) != 1 {
		t.Errorf("the new position does not: %v", got)
	}
	if p, ok := g.Get("d"); !ok || p != b {
		t.Errorf("get: %v %v", p, ok)
	}
	g.Remove("d")
	g.Remove("never-existed")
	if _, ok := g.Get("d"); ok || g.Len() != 0 || len(g.cells) != 0 {
		t.Errorf("after removal: len %d, %d cells still allocated", g.Len(), len(g.cells))
	}
}

func TestBadCoordinatesAreRefused(t *testing.T) {
	g, _ := NewGrid(areas[1].b, 500)
	if err := g.Upsert("d", Point{math.NaN(), 77.5}); err != ErrInvalidPoint {
		t.Errorf("%v", err)
	}
	if err := g.Upsert("d", Point{91, 77.5}); err != ErrInvalidPoint {
		t.Errorf("%v", err)
	}
	if err := g.Upsert("d", Point{40, 77.5}); err != ErrOutOfBounds {
		t.Errorf("a valid place outside the service area: %v", err)
	}
	if g.Len() != 0 {
		t.Error("a refused update must not leave the driver behind")
	}
	if err := NewBruteForce().Upsert("d", Point{200, 0}); err != ErrInvalidPoint {
		t.Errorf("%v", err)
	}
}

func TestTheGridLooksAtAFractionOfWhatBruteForceDoes(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	grid, _ := NewGrid(areas[1].b, 500)
	brute := NewBruteForce()
	const n = 20000
	for i := 0; i < n; i++ {
		p := randPoint(r, areas[1].b)
		id := fmt.Sprintf("d%d", i)
		grid.Upsert(id, p)
		brute.Upsert(id, p)
	}
	for i := 0; i < 200; i++ {
		q := randPoint(r, areas[1].b)
		same(t, "query", grid.Nearest(q, 5, 3000, nil), brute.Nearest(q, 5, 3000, nil))
	}
	g, b := grid.Stats(), brute.Stats()
	perGrid, perBrute := float64(g.Scanned)/float64(g.Queries), float64(b.Scanned)/float64(b.Queries)
	t.Logf("drivers examined per query: grid %.0f, brute force %.0f", perGrid, perBrute)
	if perBrute != n {
		t.Errorf("brute force should examine all %d, examined %.0f", n, perBrute)
	}
	if perGrid > 0.05*n {
		t.Errorf("the grid examined %.0f of %d drivers per query, which is not much of an index", perGrid, n)
	}
}

func TestConcurrentUpdatesAndQueriesAreSafeAndConverge(t *testing.T) {
	grid, _ := NewGrid(areas[1].b, 500)
	const workers, perWorker, steps = 8, 40, 300
	final := make([]map[string]Point, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		w := w
		final[w] = map[string]Point{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(w)))
			for s := 0; s < steps; s++ {
				id := fmt.Sprintf("w%d-%d", w, r.Intn(perWorker))
				switch r.Intn(10) {
				case 0:
					grid.Remove(id)
					delete(final[w], id)
				default:
					p := randPoint(r, areas[1].b)
					if err := grid.Upsert(id, p); err != nil {
						t.Error(err)
					}
					final[w][id] = p
				}
				if s%5 == 0 {
					grid.Nearest(randPoint(r, areas[1].b), 5, 2000, nil) // readers running while writers write
				}
			}
		}()
	}
	wg.Wait()
	brute := NewBruteForce()
	for _, m := range final {
		for id, p := range m {
			brute.Upsert(id, p)
		}
	}
	if grid.Len() != brute.Len() {
		t.Fatalf("len %d vs %d", grid.Len(), brute.Len())
	}
	r := rand.New(rand.NewSource(99))
	for i := 0; i < 50; i++ {
		q := randPoint(r, areas[1].b)
		same(t, "after the writers finish", grid.Nearest(q, 10, 0, nil), brute.Nearest(q, 10, 0, nil))
	}
}

func TestLabelsGroupNearbyPointsAndSeparateFarOnes(t *testing.T) {
	cg, _ := NewCellGrid(areas[1].b, 2000)
	a := Point{12.97, 77.59}
	if cg.Label(a) != cg.Label(Point{12.9701, 77.5901}) {
		t.Error("points a few metres apart should share a cell")
	}
	if cg.Label(a) == cg.Label(Point{13.07, 77.69}) {
		t.Error("points 15 km apart should not")
	}
}

// Two drivers the same distance away must come back in a fixed order, or the
// same request could be answered differently from one run to the next.
func TestEqualDistancesAreOrderedByID(t *testing.T) {
	for name, ix := range map[string]Index{"brute": NewBruteForce(), "grid": func() Index { g, _ := NewGrid(areas[1].b, 500); return g }()} {
		p := Point{12.97, 77.59}
		for _, id := range []string{"charlie", "alpha", "bravo"} {
			ix.Upsert(id, p)
		}
		got := ix.Nearest(Point{12.971, 77.59}, 3, 0, nil)
		if len(got) != 3 || got[0].ID != "alpha" || got[1].ID != "bravo" || got[2].ID != "charlie" {
			t.Errorf("%s: %+v", name, got)
		}
		if one := ix.Nearest(Point{12.971, 77.59}, 1, 0, nil); len(one) != 1 || one[0].ID != "alpha" {
			t.Errorf("%s: the one chosen from a tie must be the same one every time: %+v", name, one)
		}
	}
}
