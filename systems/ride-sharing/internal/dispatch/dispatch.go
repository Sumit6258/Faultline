// Package dispatch decides which driver is offered which waiting trip.
//
// Two strategies sit behind one interface so they can be compared on identical
// demand. Greedy answers each request the moment it arrives with the nearest
// free driver. Batched waits a few seconds to collect requests and then pairs
// all of them with all the nearby drivers at once. Greedy never makes a rider
// wait for a decision. Batched can sometimes save a rider a long pickup by
// leaving a driver for the person who has no other option, at the cost of that
// short wait for everyone.
//
// A strategy only proposes. Whether a proposed driver is still free is decided
// by the service when it reserves them, because the world changes between
// proposing and acting.
package dispatch

import (
	"sort"

	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/geo"
	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/match"
)

type Request struct {
	TripID string
	Pickup geo.Point
	// Attempted drivers have already turned this trip down, so they are not offered it again.
	Attempted map[string]bool
}

type Options struct {
	// Candidates is how many nearest drivers each trip considers. Batched pairs trips only with these,
	// which keeps the problem small, at the price of occasionally missing a better pairing.
	Candidates int
	MaxMeters  float64
	SpeedMps   float64
}

type Pair struct {
	TripID, DriverID string
	Meters           float64
}

type Strategy interface {
	Name() string
	// Propose pairs requests, which are in arrival order, with drivers that available reports free.
	Propose(reqs []Request, ix geo.Index, available func(driverID string) bool, o Options) []Pair
}

func accept(r Request, available func(string) bool, taken map[string]bool) func(string) bool {
	return func(id string) bool { return !taken[id] && !r.Attempted[id] && available(id) }
}

// Greedy serves requests first come, first served, each taking the nearest driver still free.
type Greedy struct{}

func (Greedy) Name() string { return "greedy" }

func (Greedy) Propose(reqs []Request, ix geo.Index, available func(string) bool, o Options) []Pair {
	var out []Pair
	taken := map[string]bool{}
	for _, r := range reqs {
		got := ix.Nearest(r.Pickup, 1, o.MaxMeters, accept(r, available, taken))
		if len(got) == 0 {
			continue
		}
		taken[got[0].ID] = true
		out = append(out, Pair{r.TripID, got[0].ID, got[0].Meters})
	}
	return out
}

// Batched pairs all the waiting requests with all the nearby free drivers at once, minimising the total pickup
// time and matching as many riders as it can.
type Batched struct{}

func (Batched) Name() string { return "batched" }

func (Batched) Propose(reqs []Request, ix geo.Index, available func(string) bool, o Options) []Pair {
	if len(reqs) == 0 {
		return nil
	}
	k := o.Candidates
	if k <= 0 {
		k = 8
	}
	cands := make([][]geo.Result, len(reqs))
	column := map[string]int{}
	var drivers []string
	for i, r := range reqs {
		cands[i] = ix.Nearest(r.Pickup, k, o.MaxMeters, accept(r, available, nil))
		for _, c := range cands[i] {
			if _, ok := column[c.ID]; !ok {
				column[c.ID] = len(drivers)
				drivers = append(drivers, c.ID)
			}
		}
	}
	if len(drivers) == 0 {
		return nil
	}
	// Columns in a fixed order, so the same inputs always give the same pairing.
	sort.Strings(drivers)
	for i, d := range drivers {
		column[d] = i
	}
	cost := make([][]float64, len(reqs))
	for i := range reqs {
		cost[i] = make([]float64, len(drivers))
		for j := range cost[i] {
			cost[i][j] = match.Forbidden
		}
		for _, c := range cands[i] {
			cost[i][column[c.ID]] = c.Meters / o.SpeedMps // seconds to reach the rider
		}
	}
	var out []Pair
	for i, j := range match.Optimal(cost) {
		if j < 0 {
			continue
		}
		for _, c := range cands[i] {
			if c.ID == drivers[j] {
				out = append(out, Pair{reqs[i].TripID, c.ID, c.Meters})
			}
		}
	}
	return out
}
