// Package ridesharing is a small ride hailing backend: drivers report where
// they are, riders ask for a trip, and the service finds a driver, offers the
// trip, and follows it to the end.
//
// The hard parts are the ones where two things happen at once. Two riders
// must not be given the same driver. A driver must not be able to accept an
// offer that has already lapsed and been given to someone else. A rider whose
// phone retries a request must not end up with two trips. All of that is
// decided here, under one lock, so it is decided the same way every time.
package ridesharing

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/clock"
	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/dispatch"
	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/geo"
	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/pricing"
	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/trip"
)

type StrategyKind string

const (
	StrategyGreedy  StrategyKind = "greedy"
	StrategyBatched StrategyKind = "batched"
)

type IndexKind string

const (
	IndexGrid  IndexKind = "grid"
	IndexBrute IndexKind = "brute"
)

type DriverState string

const (
	DriverOffline   DriverState = "offline"
	DriverAvailable DriverState = "available"
	DriverReserved  DriverState = "reserved" // an offer is out to this driver
	DriverBusy      DriverState = "busy"     // on a trip
)

// Observer is told about what happens, so metrics can be recorded without the
// service knowing about them. Calls happen while the service holds its lock,
// so an observer must be quick and must not call back into the service.
type Observer interface {
	TripRequested()
	// OfferResult is called with "offered", "accepted", "declined" or "expired".
	OfferResult(outcome string)
	// TripMatched is how long a trip waited between being requested and a driver accepting it.
	TripMatched(wait time.Duration)
	TripEnded(state trip.State)
}

type noObserver struct{}

func (noObserver) TripRequested()            {}
func (noObserver) OfferResult(string)        {}
func (noObserver) TripMatched(time.Duration) {}
func (noObserver) TripEnded(trip.State)      {}

type Config struct {
	Bounds          geo.Bounds
	CellMeters      float64 // index cell size
	SurgeCellMeters float64 // neighbourhood size for pricing
	Strategy        StrategyKind
	Index           IndexKind
	BatchWindow     time.Duration // how often the batched strategy decides
	OfferTTL        time.Duration // how long a driver has to answer
	MaxPickupMeters float64
	Candidates      int
	MaxMatchWait    time.Duration // after this a waiting trip is given up on
	DriverTTL       time.Duration // a driver not heard from for this long is treated as gone
	QuoteTTL        time.Duration
	Retention       time.Duration // how long a finished trip can still be looked up
	CancelFeeCents  int64
	Rates           pricing.Rates
	Surge           pricing.SurgeConfig
	Clock           clock.Clock
	Observer        Observer
	// OnOffer, if set, is called when a driver is offered a trip, so a real deployment can push it to the
	// driver's phone instead of waiting to be polled, and the simulation can have its drivers respond. It runs
	// while the service holds its lock, so it must be quick and must not call back into the service.
	OnOffer func(driverID, tripID string)
}

// DefaultBounds is a 28 by 32 km box around Bengaluru, an arbitrary choice that gives the service a real area to be in.
func DefaultBounds() geo.Bounds {
	return geo.Bounds{MinLat: 12.85, MaxLat: 13.10, MinLon: 77.45, MaxLon: 77.75}
}

func (c Config) withDefaults() Config {
	if c.Bounds == (geo.Bounds{}) {
		c.Bounds = DefaultBounds()
	}
	def := func(v *float64, d float64) {
		if *v == 0 {
			*v = d
		}
	}
	defD := func(v *time.Duration, d time.Duration) {
		if *v == 0 {
			*v = d
		}
	}
	def(&c.CellMeters, 500)
	def(&c.SurgeCellMeters, 2000)
	def(&c.MaxPickupMeters, 5000)
	defD(&c.BatchWindow, 2*time.Second)
	defD(&c.OfferTTL, 10*time.Second)
	defD(&c.MaxMatchWait, 2*time.Minute)
	defD(&c.DriverTTL, 30*time.Second)
	defD(&c.QuoteTTL, 2*time.Minute)
	defD(&c.Retention, 10*time.Minute)
	if c.Candidates == 0 {
		c.Candidates = 8
	}
	if c.CancelFeeCents == 0 {
		c.CancelFeeCents = 500
	}
	if c.Strategy == "" {
		c.Strategy = StrategyGreedy
	}
	if c.Index == "" {
		c.Index = IndexGrid
	}
	if c.Rates == (pricing.Rates{}) {
		c.Rates = pricing.DefaultRates()
	}
	if c.Surge == (pricing.SurgeConfig{}) {
		c.Surge = pricing.DefaultSurge()
	}
	if c.Clock == nil {
		c.Clock = clock.Real{}
	}
	if c.Observer == nil {
		c.Observer = noObserver{}
	}
	return c
}

type driver struct {
	id            string
	state         DriverState
	tripID        string
	pos           geo.Point // the last reported position, kept here so counting supply needs no index lookups
	lastSeen      time.Time
	offerDeadline time.Time
}

type Quote struct {
	ID         string
	Rider      string
	From, To   geo.Point
	Meters     float64
	FareCents  int64
	Multiplier float64
	ExpiresAt  time.Time
	usedBy     string
}

type TripRequest struct {
	Rider          string
	From, To       geo.Point
	QuoteID        string
	IdempotencyKey string
}

// TripView is a trip along with where its driver is now, which is what a rider's app draws.
type TripView struct {
	Trip           trip.Trip
	DriverLocation *geo.Point
}

type idemEntry struct {
	tripID      string
	fingerprint string
}

type Service struct {
	cfg      Config
	clk      clock.Clock
	idx      geo.Index
	cells    geo.CellGrid
	surge    *pricing.Surge
	strategy dispatch.Strategy
	obs      Observer

	mu         sync.Mutex
	drivers    map[string]*driver
	trips      map[string]trip.Trip
	pending    []string // trips waiting for a driver, in the order they arrived
	riderTrip  map[string]string
	quotes     map[string]*Quote
	idem       map[string]idemEntry
	idemKeyOf  map[string]string // trip id to its idempotency key, so the key can be dropped with the trip
	supply     map[string]int
	lastBatch  time.Time
	nextTrip   uint64
	nextQuote  uint64
	dispatches uint64
}

func New(cfg Config) (*Service, error) {
	cfg = cfg.withDefaults()
	if err := cfg.Bounds.Validate(); err != nil {
		return nil, err
	}
	var idx geo.Index
	switch cfg.Index {
	case IndexGrid:
		g, err := geo.NewGrid(cfg.Bounds, cfg.CellMeters)
		if err != nil {
			return nil, err
		}
		idx = g
	case IndexBrute:
		idx = geo.NewBruteForce()
	default:
		return nil, fmt.Errorf("ridesharing: unknown index %q, want grid or brute", cfg.Index)
	}
	var strategy dispatch.Strategy
	switch cfg.Strategy {
	case StrategyGreedy:
		strategy = dispatch.Greedy{}
	case StrategyBatched:
		strategy = dispatch.Batched{}
	default:
		return nil, fmt.Errorf("ridesharing: unknown strategy %q, want greedy or batched", cfg.Strategy)
	}
	cells, err := geo.NewCellGrid(cfg.Bounds, cfg.SurgeCellMeters)
	if err != nil {
		return nil, err
	}
	surge, err := pricing.NewSurge(cfg.Surge)
	if err != nil {
		return nil, err
	}
	if cfg.OfferTTL <= 0 || cfg.MaxMatchWait <= 0 || cfg.MaxPickupMeters <= 0 {
		return nil, fmt.Errorf("%w: offer ttl, match wait and pickup distance must be positive", ErrInvalidRequest)
	}
	return &Service{
		cfg: cfg, clk: cfg.Clock, idx: idx, cells: cells, surge: surge, strategy: strategy, obs: cfg.Observer,
		drivers: map[string]*driver{}, trips: map[string]trip.Trip{}, riderTrip: map[string]string{},
		quotes: map[string]*Quote{}, idem: map[string]idemEntry{}, idemKeyOf: map[string]string{}, supply: map[string]int{},
	}, nil
}

func (s *Service) Config() Config { return s.cfg }

func (s *Service) checkPoint(name string, p geo.Point) error {
	if !p.Valid() {
		return fmt.Errorf("%w: %s has invalid coordinates", ErrInvalidRequest, name)
	}
	if !s.cfg.Bounds.Contains(p) {
		return fmt.Errorf("%w: %s is outside the service area", ErrInvalidRequest, name)
	}
	return nil
}

func (s *Service) price(from, to geo.Point, now time.Time) (meters float64, mult float64, fare int64) {
	meters = geo.Distance(from, to)
	mult = s.surge.Multiplier(s.cells.Label(from), now)
	return meters, mult, s.cfg.Rates.Estimate(meters, mult)
}

// Quote prices a trip and holds the price for QuoteTTL, so the rider is charged what they were shown.
func (s *Service) Quote(rider string, from, to geo.Point) (Quote, error) {
	if rider == "" {
		return Quote{}, fmt.Errorf("%w: rider is required", ErrInvalidRequest)
	}
	if err := s.checkPoint("pickup", from); err != nil {
		return Quote{}, err
	}
	if err := s.checkPoint("dropoff", to); err != nil {
		return Quote{}, err
	}
	now := s.clk.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	meters, mult, fare := s.price(from, to, now)
	s.nextQuote++
	q := &Quote{ID: fmt.Sprintf("quote-%06d", s.nextQuote), Rider: rider, From: from, To: to, Meters: meters, FareCents: fare, Multiplier: mult, ExpiresAt: now.Add(s.cfg.QuoteTTL)}
	s.quotes[q.ID] = q
	return *q, nil
}

func fingerprint(r TripRequest) string {
	return fmt.Sprintf("%.6f,%.6f>%.6f,%.6f|%s", r.From.Lat, r.From.Lon, r.To.Lat, r.To.Lon, r.QuoteID)
}

// RequestTrip creates a trip and starts looking for a driver. created is false when the request repeats an
// earlier one with the same idempotency key, in which case the earlier trip is returned.
func (s *Service) RequestTrip(req TripRequest) (tr trip.Trip, created bool, err error) {
	if req.Rider == "" {
		return trip.Trip{}, false, fmt.Errorf("%w: rider is required", ErrInvalidRequest)
	}
	if err := s.checkPoint("pickup", req.From); err != nil {
		return trip.Trip{}, false, err
	}
	if err := s.checkPoint("dropoff", req.To); err != nil {
		return trip.Trip{}, false, err
	}
	if geo.Distance(req.From, req.To) < 100 {
		return trip.Trip{}, false, fmt.Errorf("%w: pickup and dropoff are less than 100 m apart", ErrInvalidRequest)
	}
	now := s.clk.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	idemKey := ""
	if req.IdempotencyKey != "" {
		idemKey = req.Rider + "\x00" + req.IdempotencyKey
		if e, ok := s.idem[idemKey]; ok {
			if e.fingerprint != fingerprint(req) {
				return trip.Trip{}, false, ErrIdempotencyConflict
			}
			if prior, ok := s.trips[e.tripID]; ok {
				return prior, false, nil
			}
			// the earlier trip has aged out, so this is a new request after all
		}
	}
	if id, ok := s.riderTrip[req.Rider]; ok {
		if cur, ok := s.trips[id]; ok && !cur.State.Terminal() {
			return trip.Trip{}, false, ErrRiderBusy
		}
	}

	var fare int64
	var mult float64
	var q *Quote
	if req.QuoteID != "" {
		q = s.quotes[req.QuoteID]
		switch {
		case q == nil || q.Rider != req.Rider:
			return trip.Trip{}, false, ErrQuoteNotFound
		case now.After(q.ExpiresAt):
			return trip.Trip{}, false, ErrQuoteExpired
		case q.usedBy != "":
			return trip.Trip{}, false, ErrQuoteUsed
		case q.From != req.From || q.To != req.To:
			return trip.Trip{}, false, ErrQuoteMismatch
		}
		fare, mult = q.FareCents, q.Multiplier
	} else {
		_, mult, fare = s.price(req.From, req.To, now)
	}

	s.nextTrip++
	id := fmt.Sprintf("trip-%06d", s.nextTrip)
	t := trip.New(id, req.Rider, req.From, req.To, req.QuoteID, fare, mult, now)
	s.trips[id] = t
	s.pending = append(s.pending, id)
	s.riderTrip[req.Rider] = id
	if q != nil {
		q.usedBy = id
	}
	if idemKey != "" {
		s.idem[idemKey] = idemEntry{tripID: id, fingerprint: fingerprint(req)}
		s.idemKeyOf[id] = idemKey
	}
	s.surge.RecordRequest(s.cells.Label(req.From), now)
	s.obs.TripRequested()
	if s.cfg.Strategy == StrategyGreedy {
		s.dispatchLocked(now)
	}
	return s.trips[id], true, nil
}

func (s *Service) GetTrip(id string) (TripView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.trips[id]
	if !ok {
		return TripView{}, ErrTripNotFound
	}
	v := TripView{Trip: t}
	if t.Driver != "" && !t.State.Terminal() {
		if p, ok := s.idx.Get(t.Driver); ok {
			v.DriverLocation = &p
		}
	}
	return v, nil
}

// CancelTrip cancels on behalf of "rider" or "system". A rider who cancels after a driver has committed is charged
// the cancellation fee, and the driver is freed to take someone else.
func (s *Service) CancelTrip(id, by string) (trip.Trip, error) {
	if by != "rider" && by != "system" {
		return trip.Trip{}, fmt.Errorf("%w: cancelled by must be rider or system", ErrInvalidRequest)
	}
	now := s.clk.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.trips[id]
	if !ok {
		return trip.Trip{}, ErrTripNotFound
	}
	next, changed, err := t.Apply(trip.Event{Kind: trip.EvCancel, At: now, By: by, FeeCents: s.cfg.CancelFeeCents})
	if err != nil {
		return t, conflict(err)
	}
	if changed {
		s.commitLocked(next, now)
		s.releaseDriverLocked(next.Driver, next.ID)
	}
	return next, nil
}

// commitLocked stores a trip after a change and does the bookkeeping for trips that have ended.
func (s *Service) commitLocked(t trip.Trip, now time.Time) {
	s.trips[t.ID] = t
	if t.State == trip.Requested {
		s.queueLocked(t.ID)
	}
	if t.State.Terminal() {
		if s.riderTrip[t.Rider] == t.ID {
			delete(s.riderTrip, t.Rider)
		}
		s.obs.TripEnded(t.State)
	}
}

func (s *Service) queueLocked(id string) {
	for _, p := range s.pending {
		if p == id {
			return
		}
	}
	s.pending = append(s.pending, id)
}

func (s *Service) releaseDriverLocked(driverID, tripID string) {
	if driverID == "" {
		return
	}
	if d := s.drivers[driverID]; d != nil && d.tripID == tripID && (d.state == DriverReserved || d.state == DriverBusy) {
		d.state, d.tripID, d.offerDeadline = DriverAvailable, "", time.Time{}
	}
}

func conflict(err error) error {
	return fmt.Errorf("%w: %v", ErrConflict, err)
}

// sortedPending returns waiting trips, oldest first, dropping any that are no longer waiting.
func (s *Service) sortedPendingLocked() []trip.Trip {
	live := s.pending[:0]
	out := make([]trip.Trip, 0, len(s.pending))
	for _, id := range s.pending {
		if t, ok := s.trips[id]; ok && t.State == trip.Requested {
			live = append(live, id)
			out = append(out, t)
		}
	}
	s.pending = live
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}
