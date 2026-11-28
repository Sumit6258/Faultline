package dispatch

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/geo"
)

var area = geo.Bounds{MinLat: 12.85, MaxLat: 13.10, MinLon: 77.45, MaxLon: 77.75}

func opts() Options { return Options{Candidates: 50, MaxMeters: 6000, SpeedMps: 8.3} }

func world(drivers map[string]geo.Point) geo.Index {
	g, _ := geo.NewGrid(area, 500)
	for id, p := range drivers {
		g.Upsert(id, p)
	}
	return g
}

func all(string) bool { return true }

func pt(lat, lon float64) geo.Point { return geo.Point{Lat: lat, Lon: lon} }

func TestGreedyGivesEachRequestTheNearestFreeDriver(t *testing.T) {
	ix := world(map[string]geo.Point{"near": pt(12.9701, 77.59), "mid": pt(12.98, 77.59), "far": pt(13.02, 77.59)})
	reqs := []Request{{TripID: "t1", Pickup: geo.Point{Lat: 12.97, Lon: 77.59}}, {TripID: "t2", Pickup: geo.Point{Lat: 12.97, Lon: 77.59}}}
	got := Greedy{}.Propose(reqs, ix, all, opts())
	if len(got) != 2 || got[0].DriverID != "near" || got[1].DriverID != "mid" {
		t.Errorf("first come, first served: %+v", got)
	}
}

func TestNoDriverIsProposedTwiceInOneRound(t *testing.T) {
	ix := world(map[string]geo.Point{"only": pt(12.97, 77.59)})
	reqs := []Request{{TripID: "t1", Pickup: geo.Point{Lat: 12.97, Lon: 77.59}}, {TripID: "t2", Pickup: geo.Point{Lat: 12.97, Lon: 77.59}}, {TripID: "t3", Pickup: geo.Point{Lat: 12.97, Lon: 77.59}}}
	for _, s := range []Strategy{Greedy{}, Batched{}} {
		if got := s.Propose(reqs, ix, all, opts()); len(got) != 1 || got[0].DriverID != "only" {
			t.Errorf("%s: %+v", s.Name(), got)
		}
	}
}

func TestBusyAndAlreadyRefusedDriversAreSkipped(t *testing.T) {
	ix := world(map[string]geo.Point{"busy": pt(12.9701, 77.59), "refused": pt(12.9702, 77.59), "free": pt(12.99, 77.59)})
	reqs := []Request{{TripID: "t1", Pickup: geo.Point{Lat: 12.97, Lon: 77.59}, Attempted: map[string]bool{"refused": true}}}
	for _, s := range []Strategy{Greedy{}, Batched{}} {
		got := s.Propose(reqs, ix, func(id string) bool { return id != "busy" }, opts())
		if len(got) != 1 || got[0].DriverID != "free" {
			t.Errorf("%s: %+v", s.Name(), got)
		}
	}
}

func TestDriversBeyondTheMaximumPickupDistanceAreNotProposed(t *testing.T) {
	ix := world(map[string]geo.Point{"far": pt(13.05, 77.59)}) // about 9 km
	reqs := []Request{{TripID: "t1", Pickup: geo.Point{Lat: 12.97, Lon: 77.59}}}
	for _, s := range []Strategy{Greedy{}, Batched{}} {
		if got := s.Propose(reqs, ix, all, opts()); len(got) != 0 {
			t.Errorf("%s: %+v", s.Name(), got)
		}
	}
}

func TestNothingToDo(t *testing.T) {
	ix := world(nil)
	for _, s := range []Strategy{Greedy{}, Batched{}} {
		if got := s.Propose(nil, ix, all, opts()); len(got) != 0 {
			t.Errorf("%s with no requests: %+v", s.Name(), got)
		}
		if got := s.Propose([]Request{{TripID: "t", Pickup: geo.Point{Lat: 12.9, Lon: 77.5}}}, ix, all, opts()); len(got) != 0 {
			t.Errorf("%s with no drivers: %+v", s.Name(), got)
		}
	}
}

// Rider A is a little nearer driver 1 than driver 2, but driver 1 is the only one anywhere near rider B. Greedy,
// serving A first, takes driver 1 and sends B a far driver. Batched sees both riders and sends driver 2 to A.
func TestBatchingSavesTheRiderWhoHasNoOtherOption(t *testing.T) {
	ix := world(map[string]geo.Point{
		"d1": pt(12.9700, 77.5900),
		"d2": pt(12.9745, 77.5900), // 500 m north of d1
	})
	reqs := []Request{
		{TripID: "A", Pickup: geo.Point{Lat: 12.9718, Lon: 77.5900}}, // 200 m north of d1, 300 m south of d2
		{TripID: "B", Pickup: geo.Point{Lat: 12.9680, Lon: 77.5900}}, // 220 m south of d1, 720 m from d2
	}
	total := func(ps []Pair) (m float64, n int) {
		for _, p := range ps {
			m += p.Meters
			n++
		}
		return
	}
	g := Greedy{}.Propose(reqs, ix, all, opts())
	b := Batched{}.Propose(reqs, ix, all, opts())
	gm, gn := total(g)
	bm, bn := total(b)
	if gn != 2 || bn != 2 {
		t.Fatalf("both should match both riders: greedy %d, batched %d", gn, bn)
	}
	t.Logf("total pickup distance: greedy %.0f m, batched %.0f m", gm, bm)
	if !(bm < gm) {
		t.Errorf("batched should have a shorter total pickup, greedy %.0f m and batched %.0f m", gm, bm)
	}
}

func TestBatchedIsNeverWorseThanGreedyOnRandomCities(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	better := 0
	for run := 0; run < 150; run++ {
		drivers := map[string]geo.Point{}
		for i := 0; i < 5+r.Intn(30); i++ {
			drivers[fmt.Sprintf("d%02d", i)] = geo.Point{Lat: 12.95 + r.Float64()*0.04, Lon: 77.55 + r.Float64()*0.06}
		}
		ix := world(drivers)
		var reqs []Request
		for i := 0; i < 1+r.Intn(25); i++ {
			reqs = append(reqs, Request{TripID: fmt.Sprintf("t%02d", i), Pickup: geo.Point{Lat: 12.95 + r.Float64()*0.04, Lon: 77.55 + r.Float64()*0.06}})
		}
		g := Greedy{}.Propose(reqs, ix, all, opts())
		b := Batched{}.Propose(reqs, ix, all, opts())
		sum := func(ps []Pair) (m float64) {
			for _, p := range ps {
				m += p.Meters
			}
			return
		}
		seen := map[string]bool{}
		for _, p := range b {
			if seen[p.DriverID] {
				t.Fatalf("batched proposed %s twice", p.DriverID)
			}
			seen[p.DriverID] = true
		}
		if len(b) < len(g) || (len(b) == len(g) && sum(b) > sum(g)+1e-6) {
			t.Fatalf("run %d: greedy matched %d for %.0f m, batched %d for %.0f m", run, len(g), sum(g), len(b), sum(b))
		}
		if len(b) > len(g) || sum(b) < sum(g)-1e-6 {
			better++
		}
	}
	if better < 30 {
		t.Errorf("batched should win a good share of random cases, won only %d of 150", better)
	}
}

func TestTheSameInputsGiveTheSamePairing(t *testing.T) {
	r := rand.New(rand.NewSource(8))
	drivers := map[string]geo.Point{}
	for i := 0; i < 40; i++ {
		drivers[fmt.Sprintf("d%02d", i)] = geo.Point{Lat: 12.95 + r.Float64()*0.04, Lon: 77.55 + r.Float64()*0.06}
	}
	ix := world(drivers)
	var reqs []Request
	for i := 0; i < 20; i++ {
		reqs = append(reqs, Request{TripID: fmt.Sprintf("t%02d", i), Pickup: geo.Point{Lat: 12.95 + r.Float64()*0.04, Lon: 77.55 + r.Float64()*0.06}})
	}
	first := fmt.Sprint(Batched{}.Propose(reqs, ix, all, opts()))
	for i := 0; i < 20; i++ {
		if again := fmt.Sprint(Batched{}.Propose(reqs, ix, all, opts())); again != first {
			t.Fatalf("different pairings from identical input:\n%s\n%s", first, again)
		}
	}
}

func BenchmarkPropose(b *testing.B) {
	r := rand.New(rand.NewSource(1))
	drivers := map[string]geo.Point{}
	for i := 0; i < 2000; i++ {
		drivers[fmt.Sprintf("d%04d", i)] = geo.Point{Lat: 12.85 + r.Float64()*0.25, Lon: 77.45 + r.Float64()*0.30}
	}
	ix := world(drivers)
	for _, n := range []int{10, 50, 150} {
		var reqs []Request
		for i := 0; i < n; i++ {
			reqs = append(reqs, Request{TripID: fmt.Sprintf("t%03d", i), Pickup: geo.Point{Lat: 12.85 + r.Float64()*0.25, Lon: 77.45 + r.Float64()*0.30}})
		}
		for _, s := range []Strategy{Greedy{}, Batched{}} {
			b.Run(fmt.Sprintf("%s/waiting=%d", s.Name(), n), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					s.Propose(reqs, ix, all, Options{Candidates: 8, MaxMeters: 5000, SpeedMps: 8.3})
				}
			})
		}
	}
}
