// Package sim runs the real dispatcher on a city of simulated riders and
// drivers, on a clock it controls, so strategies can be compared on exactly
// the same demand.
//
// It drives the service through its public methods and nothing else, so what
// it measures is the code that would run in production, not a model of it.
// Only the people are modelled: riders appear at random, drivers answer offers
// after a delay and sometimes decline or ignore them, and trips take as long as
// the distance divided by a constant speed.
//
// Two runs with the same parameters are identical, and runs that differ only
// in strategy see the same riders asking for the same trips, and the same
// drivers making the same choices when offered the same trip. That is what
// lets a difference between strategies be put down to the strategy.
package sim

import (
	"container/heap"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"time"

	ridesharing "github.com/Sumit6258/Faultline/systems/ride-sharing"
	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/clock"
	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/geo"
	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/trip"
)

type Params struct {
	Seed            int64
	Drivers         int
	RidersPerMinute float64
	Duration        time.Duration // simulated time during which riders keep arriving
	Warmup          time.Duration // trips requested before this are simulated but not counted
	Strategy        ridesharing.StrategyKind
	BatchWindow     time.Duration
	Index           ridesharing.IndexKind
	AcceptProb      float64 // of the offers a driver answers, the share they accept
	IgnoreProb      float64 // the share of offers a driver never answers
	HalfSizeMeters  float64 // the city is a square this far from the center in each direction
	HotspotShare    float64 // the share of requests that come from a hotspot instead of anywhere
	Hotspots        int
	SpeedMps        float64
	MaxPickupMeters float64
}

func (p Params) withDefaults() Params {
	if p.Drivers == 0 {
		p.Drivers = 200
	}
	if p.RidersPerMinute == 0 {
		p.RidersPerMinute = 30
	}
	if p.Duration == 0 {
		p.Duration = 2 * time.Hour
	}
	if p.Warmup == 0 {
		p.Warmup = 10 * time.Minute
	}
	if p.Strategy == "" {
		p.Strategy = ridesharing.StrategyGreedy
	}
	if p.BatchWindow == 0 {
		p.BatchWindow = 2 * time.Second
	}
	if p.Index == "" {
		p.Index = ridesharing.IndexGrid
	}
	if p.AcceptProb == 0 {
		p.AcceptProb = 0.9
	}
	if p.IgnoreProb == 0 {
		p.IgnoreProb = 0.03
	}
	if p.HalfSizeMeters == 0 {
		p.HalfSizeMeters = 4000
	}
	if p.HotspotShare == 0 {
		p.HotspotShare = 0.4
	}
	if p.Hotspots == 0 {
		p.Hotspots = 3
	}
	if p.SpeedMps == 0 {
		p.SpeedMps = 8.3
	}
	if p.MaxPickupMeters == 0 {
		p.MaxPickupMeters = 5000
	}
	return p
}

type Summary struct {
	N              int
	Mean, P50, P95 float64
}

func summarize(xs []float64) Summary {
	if len(xs) == 0 {
		return Summary{}
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	var sum float64
	for _, x := range s {
		sum += x
	}
	q := func(p float64) float64 {
		k := float64(len(s)-1) * p
		lo, hi := int(math.Floor(k)), int(math.Ceil(k))
		return s[lo] + (s[hi]-s[lo])*(k-float64(lo))
	}
	return Summary{N: len(s), Mean: sum / float64(len(s)), P50: q(0.5), P95: q(0.95)}
}

// Summarize is exported so results from several seeds can be pooled.
func Summarize(xs []float64) Summary { return summarize(xs) }

type Result struct {
	Params
	Requested  int // counted trips: requested after the warm up
	Completed  int
	NoDrivers  int
	Unfinished int // still waiting or underway when the simulation ended, which should be none

	MatchWait    Summary // seconds from asking to a driver accepting
	PickupETA    Summary // seconds from accepting to the driver arriving
	TimeToPickup Summary // the two together: what the rider actually waits
	// The raw samples, so several seeds can be pooled into one summary.
	MatchWaitSamples, PickupSamples, TimeToPickupSamples []float64

	OffersPerTrip  float64 // offers made per trip requested, counting every trip, so 1 means every first offer was taken
	DeadheadMeters float64 // mean distance a driver drove empty to reach a rider
	Utilization    float64 // the share of driver time spent heading to or carrying a rider
	Wall           time.Duration
}

type kind int

const (
	evArrival kind = iota
	evTick
	evRespond
	evPickup
	evStartRide
	evComplete
)

type event struct {
	at     time.Duration
	seq    int
	kind   kind
	idx    int // for arrivals: which rider
	driver string
	tripID string
	accept bool
}

type queue []event

func (q queue) Len() int { return len(q) }
func (q queue) Less(i, j int) bool {
	if q[i].at != q[j].at {
		return q[i].at < q[j].at
	}
	return q[i].seq < q[j].seq
}
func (q queue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *queue) Push(x any)   { *q = append(*q, x.(event)) }
func (q *queue) Pop() any {
	old := *q
	n := len(old)
	x := old[n-1]
	*q = old[:n-1]
	return x
}

type request struct {
	at              time.Duration
	pickup, dropoff geo.Point
}

type tinfo struct {
	idx         int
	attempts    int
	requestedAt time.Duration
	counted     bool
	driver      string
	acceptedAt  time.Duration
	pickupETA   float64
	deadhead    float64 // meters the driver drove empty to reach the rider
	pickup      geo.Point
	dropoff     geo.Point
}

type counter struct{ offered, requested int }

func (c *counter) TripRequested() { c.requested++ }
func (c *counter) OfferResult(o string) {
	if o == "offered" {
		c.offered++
	}
}
func (c *counter) TripMatched(time.Duration) {}
func (c *counter) TripEnded(trip.State)      {}

func offset(p geo.Point, northM, eastM float64) geo.Point {
	return geo.Point{Lat: p.Lat + northM/geo.MetersPerDegreeLat, Lon: p.Lon + eastM/(geo.MetersPerDegreeLat*math.Cos(p.Lat*math.Pi/180))}
}

type world struct {
	center   geo.Point
	half     float64
	hotspots []geo.Point
	share    float64
}

func (w world) uniform(r *rand.Rand) geo.Point {
	return offset(w.center, (r.Float64()*2-1)*w.half, (r.Float64()*2-1)*w.half)
}

func (w world) inside(p geo.Point) bool {
	north := (p.Lat - w.center.Lat) * geo.MetersPerDegreeLat
	east := (p.Lon - w.center.Lon) * geo.MetersPerDegreeLat * math.Cos(w.center.Lat*math.Pi/180)
	return math.Abs(north) <= w.half && math.Abs(east) <= w.half
}

// demand draws where someone asks to be picked up: usually anywhere, sometimes near a hotspot. Supply is spread
// evenly, so hotspots are where there are too few drivers and too many riders, which is where the choice of
// strategy matters.
func (w world) demand(r *rand.Rand) geo.Point {
	if r.Float64() < w.share {
		h := w.hotspots[r.Intn(len(w.hotspots))]
		for i := 0; i < 20; i++ {
			p := offset(h, r.NormFloat64()*600, r.NormFloat64()*600)
			if w.inside(p) {
				return p
			}
		}
		return h
	}
	return w.uniform(r)
}

type simulation struct {
	p        Params
	w        world
	svc      *ridesharing.Service
	clk      *clock.Fake
	t0       time.Time
	now      time.Duration
	q        queue
	seq      int
	reqs     []request
	trips    map[string]*tinfo
	pos      map[string]geo.Point
	arriving int
	obs      *counter
	busy     float64 // driver seconds on a trip or heading to one, within the counted window
	res      Result
}

func (s *simulation) at(d time.Duration, e event) {
	s.seq++
	e.at, e.seq = d, s.seq
	heap.Push(&s.q, e)
}

// draw is what a driver does when offered the trip numbered idx for the attempt'th time. It depends on nothing
// but the seed, the trip and the attempt, so every strategy faces the same behaviour.
func (s *simulation) draw(idx, attempt int) (delay time.Duration, accept, ignore bool) {
	r := rand.New(rand.NewSource(s.p.Seed*1_000_003 + int64(idx)*131 + int64(attempt)*7919 + 17))
	delay = time.Duration((2 + r.Float64()*4) * float64(time.Second))
	ignore = r.Float64() < s.p.IgnoreProb
	accept = r.Float64() < s.p.AcceptProb
	return
}

func (s *simulation) clipped(a, b time.Duration) float64 {
	lo, hi := a, b
	if lo < s.p.Warmup {
		lo = s.p.Warmup
	}
	if hi > s.p.Duration {
		hi = s.p.Duration
	}
	if hi <= lo {
		return 0
	}
	return (hi - lo).Seconds()
}

// Run simulates one configuration and reports what the riders experienced.
func Run(p Params) (Result, error) {
	p = p.withDefaults()
	wall := time.Now()
	center := ridesharing.DefaultBounds().Center()
	rng := rand.New(rand.NewSource(p.Seed))
	w := world{center: center, half: p.HalfSizeMeters, share: p.HotspotShare}
	for i := 0; i < p.Hotspots; i++ {
		w.hotspots = append(w.hotspots, offset(center, (rng.Float64()*2-1)*p.HalfSizeMeters*0.6, (rng.Float64()*2-1)*p.HalfSizeMeters*0.6))
	}

	s := &simulation{p: p, w: w, trips: map[string]*tinfo{}, pos: map[string]geo.Point{}, obs: &counter{},
		t0: time.Date(2026, 11, 2, 18, 0, 0, 0, time.UTC)}
	s.clk = clock.NewFake(s.t0)
	var err error
	s.svc, err = ridesharing.New(ridesharing.Config{
		Strategy: p.Strategy, Index: p.Index, BatchWindow: p.BatchWindow, MaxPickupMeters: p.MaxPickupMeters,
		Clock: s.clk, Observer: s.obs, OnOffer: s.onOffer,
		DriverTTL: 1000 * time.Hour, Retention: 1000 * time.Hour, QuoteTTL: time.Hour,
	})
	if err != nil {
		return Result{}, err
	}

	for i := 0; i < p.Drivers; i++ {
		id := fmt.Sprintf("d%04d", i)
		s.pos[id] = w.uniform(rng)
		if err := s.svc.UpdateLocation(id, s.pos[id]); err != nil {
			return Result{}, err
		}
	}
	// The riders for the whole run, drawn up front from their own stream, so they are the same whatever the strategy does.
	arrivals := rand.New(rand.NewSource(p.Seed + 1))
	for t := 0.0; ; {
		t += arrivals.ExpFloat64() * 60 / p.RidersPerMinute
		at := time.Duration(t * float64(time.Second))
		if at >= p.Duration {
			break
		}
		pick := w.demand(arrivals)
		drop := w.uniform(arrivals)
		for geo.Distance(pick, drop) < 800 {
			drop = w.uniform(arrivals)
		}
		s.reqs = append(s.reqs, request{at, pick, drop})
	}
	for i, r := range s.reqs {
		s.at(r.at, event{kind: evArrival, idx: i})
	}
	drain := 15 * time.Minute // after the last rider appears, until everything under way has finished
	for t := time.Duration(0); t <= p.Duration+drain; t += time.Second {
		s.at(t, event{kind: evTick})
	}

	for s.q.Len() > 0 {
		e := heap.Pop(&s.q).(event)
		s.now = e.at
		s.clk.Set(s.t0.Add(e.at))
		if err := s.handle(e); err != nil {
			return Result{}, err
		}
	}

	r := &s.res
	r.Params = p
	var matched int
	var deadhead float64
	// In a fixed order: floating point addition depends on the order, and Go randomises map iteration.
	ids := make([]string, 0, len(s.trips))
	for id := range s.trips {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		ti := s.trips[id]
		if !ti.counted {
			continue
		}
		v, err := s.svc.GetTrip(id)
		if err != nil {
			return Result{}, err
		}
		switch v.Trip.State {
		case trip.Completed:
			r.Completed++
		case trip.NoDrivers:
			r.NoDrivers++
		default:
			r.Unfinished++
		}
		if ti.driver != "" {
			matched++
			wait := (ti.acceptedAt - ti.requestedAt).Seconds()
			r.MatchWaitSamples = append(r.MatchWaitSamples, wait)
			r.PickupSamples = append(r.PickupSamples, ti.pickupETA)
			r.TimeToPickupSamples = append(r.TimeToPickupSamples, wait+ti.pickupETA)
			deadhead += ti.deadhead
		}
	}
	for _, q := range s.reqs {
		if q.at >= p.Warmup {
			r.Requested++
		}
	}
	// Map order must not decide the order of the samples, or two identical runs could differ in the last digit.
	sort.Float64s(r.MatchWaitSamples)
	sort.Float64s(r.PickupSamples)
	sort.Float64s(r.TimeToPickupSamples)
	r.MatchWait, r.PickupETA, r.TimeToPickup = summarize(r.MatchWaitSamples), summarize(r.PickupSamples), summarize(r.TimeToPickupSamples)
	if matched > 0 {
		r.DeadheadMeters = deadhead / float64(matched)
	}
	if s.obs.requested > 0 {
		r.OffersPerTrip = float64(s.obs.offered) / float64(s.obs.requested)
	}
	r.Utilization = s.busy / (float64(p.Drivers) * (p.Duration - p.Warmup).Seconds())
	r.Wall = time.Since(wall)
	return *r, nil
}

// onOffer runs inside the service, under its lock. It only schedules what the driver will do, and never calls back.
func (s *simulation) onOffer(driverID, tripID string) {
	ti := s.trips[tripID]
	if ti == nil { // the first offer, made while the request is still being handled
		req := s.reqs[s.arriving]
		ti = &tinfo{idx: s.arriving, requestedAt: req.at, counted: req.at >= s.p.Warmup, pickup: req.pickup, dropoff: req.dropoff}
		s.trips[tripID] = ti
	}
	delay, accept, ignore := s.draw(ti.idx, ti.attempts)
	ti.attempts++
	if ignore {
		return // never answers: the offer lapses on its own
	}
	s.at(s.now+delay, event{kind: evRespond, driver: driverID, tripID: tripID, accept: accept})
}

func (s *simulation) handle(e event) error {
	switch e.kind {
	case evTick:
		s.svc.Tick()
	case evArrival:
		s.arriving = e.idx
		req := s.reqs[e.idx]
		tr, _, err := s.svc.RequestTrip(ridesharing.TripRequest{Rider: fmt.Sprintf("r%06d", e.idx), From: req.pickup, To: req.dropoff})
		if err != nil {
			return fmt.Errorf("request %d: %w", e.idx, err)
		}
		if s.trips[tr.ID] == nil { // no offer was made yet
			s.trips[tr.ID] = &tinfo{idx: e.idx, requestedAt: req.at, counted: req.at >= s.p.Warmup, pickup: req.pickup, dropoff: req.dropoff}
		}
	case evRespond:
		ti := s.trips[e.tripID]
		if !e.accept {
			s.svc.DeclineOffer(e.driver, e.tripID)
			return nil
		}
		if _, err := s.svc.AcceptOffer(e.driver, e.tripID); err != nil {
			return nil // the offer lapsed or was reassigned first, which is what a slow driver gets
		}
		dist := geo.Distance(s.pos[e.driver], ti.pickup)
		ti.driver, ti.acceptedAt = e.driver, s.now
		ti.pickupETA, ti.deadhead = dist/s.p.SpeedMps, dist
		s.at(s.now+time.Duration(ti.pickupETA*float64(time.Second)), event{kind: evPickup, driver: e.driver, tripID: e.tripID})
	case evPickup:
		s.svc.ArriveAtPickup(e.driver, e.tripID)
		s.at(s.now+20*time.Second, event{kind: evStartRide, driver: e.driver, tripID: e.tripID})
	case evStartRide:
		ti := s.trips[e.tripID]
		s.svc.StartTrip(e.driver, e.tripID)
		ride := geo.Distance(ti.pickup, ti.dropoff) / s.p.SpeedMps
		s.at(s.now+time.Duration(ride*float64(time.Second)), event{kind: evComplete, driver: e.driver, tripID: e.tripID})
	case evComplete:
		ti := s.trips[e.tripID]
		s.svc.CompleteTrip(e.driver, e.tripID)
		s.pos[e.driver] = ti.dropoff
		s.busy += s.clipped(ti.acceptedAt, s.now)
		if err := s.svc.UpdateLocation(e.driver, ti.dropoff); err != nil {
			return err
		}
	}
	return nil
}
