package ridesharing

import (
	"sort"
	"time"

	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/dispatch"
	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/geo"
	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/trip"
)

func (s *Service) isAvailable(id string) bool {
	d := s.drivers[id]
	return d != nil && d.state == DriverAvailable
}

// dispatchLocked asks the strategy who should be offered what, and acts on its answer. The strategy only
// advises. The driver is reserved here, under the lock, and only if still free, so two trips can never be
// offered the same driver however the strategy behaves.
func (s *Service) dispatchLocked(now time.Time) {
	waiting := s.sortedPendingLocked()
	if len(waiting) == 0 {
		return
	}
	reqs := make([]dispatch.Request, len(waiting))
	for i, t := range waiting {
		var attempted map[string]bool
		if len(t.Attempted) > 0 {
			attempted = make(map[string]bool, len(t.Attempted))
			for _, d := range t.Attempted {
				attempted[d] = true
			}
		}
		reqs[i] = dispatch.Request{TripID: t.ID, Pickup: t.Pickup, Attempted: attempted}
	}
	s.dispatches++
	pairs := s.strategy.Propose(reqs, s.idx, s.isAvailable, dispatch.Options{
		Candidates: s.cfg.Candidates, MaxMeters: s.cfg.MaxPickupMeters, SpeedMps: s.cfg.Rates.SpeedMps})
	for _, p := range pairs {
		s.offerLocked(p.TripID, p.DriverID, now)
	}
}

// offerLocked reserves a driver for a trip and records the offer. It is the one place a driver goes from
// available to reserved, and it checks they still are available.
func (s *Service) offerLocked(tripID, driverID string, now time.Time) {
	d := s.drivers[driverID]
	t, ok := s.trips[tripID]
	if d == nil || d.state != DriverAvailable || !ok || t.State != trip.Requested {
		return
	}
	deadline := now.Add(s.cfg.OfferTTL)
	next, changed, err := t.Apply(trip.Event{Kind: trip.EvOffer, At: now, Driver: driverID, Deadline: deadline})
	if err != nil || !changed {
		return
	}
	d.state, d.tripID, d.offerDeadline = DriverReserved, tripID, deadline
	s.trips[tripID] = next
	s.obs.OfferResult("offered")
	if s.cfg.OnOffer != nil {
		s.cfg.OnOffer(driverID, tripID)
	}
}

// expireOfferLocked is an offer lapsing: the driver is free again and the trip goes back to waiting.
func (s *Service) expireOfferLocked(d *driver, now time.Time) {
	if t, ok := s.trips[d.tripID]; ok {
		if next, changed, err := t.Apply(trip.Event{Kind: trip.EvOfferExpired, At: now, Driver: d.id}); err == nil && changed {
			s.commitLocked(next, now)
			s.obs.OfferResult("expired")
		}
	}
	s.freeDriverLocked(d)
}

// Tick is the service's clock. It lapses offers, drops drivers that have gone quiet, gives up on trips that have
// waited too long, runs the matching, and updates supply for pricing. The server calls it a few times a second.
func (s *Service) Tick() {
	now := s.clk.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	var lapsed, stale []string
	for id, d := range s.drivers {
		switch {
		case d.state == DriverReserved && !now.Before(d.offerDeadline):
			lapsed = append(lapsed, id)
		case d.state != DriverOffline && d.state != DriverBusy && now.Sub(d.lastSeen) > s.cfg.DriverTTL:
			stale = append(stale, id)
		}
	}
	// In a fixed order, so a run is the same every time.
	sort.Strings(lapsed)
	sort.Strings(stale)
	for _, id := range lapsed {
		s.expireOfferLocked(s.drivers[id], now)
	}
	for _, id := range stale {
		d := s.drivers[id]
		if d.state == DriverReserved {
			s.expireOfferLocked(d, now)
		}
		d.state, d.tripID = DriverOffline, ""
		s.idx.Remove(id)
	}

	for _, t := range s.sortedPendingLocked() {
		if now.Sub(t.CreatedAt) >= s.cfg.MaxMatchWait {
			if next, changed, err := t.Apply(trip.Event{Kind: trip.EvNoDrivers, At: now, By: "system"}); err == nil && changed {
				s.commitLocked(next, now)
			}
		}
	}

	if s.cfg.Strategy == StrategyGreedy || now.Sub(s.lastBatch) >= s.cfg.BatchWindow {
		s.dispatchLocked(now)
		s.lastBatch = now
	}

	s.updateSupplyLocked()
	s.pruneLocked(now)
}

// updateSupplyLocked tells pricing how many drivers are free in each neighbourhood. It counts by cell
// coordinates and names only the cells that have a driver in them: naming a cell for every driver cost an
// allocation per driver on every tick, which the Tick benchmark made plain.
func (s *Service) updateSupplyLocked() {
	type cell struct{ x, y int }
	counts := map[cell]int{}
	for _, d := range s.drivers {
		if d.state == DriverAvailable {
			x, y := s.cells.Cell(d.pos)
			counts[cell{x, y}]++
		}
	}
	named := make(map[string]int, len(counts))
	for c, n := range counts {
		named[geo.LabelOf(c.x, c.y)] = n
	}
	for name := range s.supply {
		if _, ok := named[name]; !ok {
			s.surge.SetSupply(name, 0)
		}
	}
	for name, n := range named {
		s.surge.SetSupply(name, n)
	}
	s.supply = named
}

// pruneLocked forgets finished trips, old quotes and their idempotency keys, so a service that runs for weeks
// does not grow without bound. A finished trip can be looked up for Retention afterwards.
func (s *Service) pruneLocked(now time.Time) {
	for id, t := range s.trips {
		if t.State.Terminal() && now.Sub(t.UpdatedAt) > s.cfg.Retention {
			delete(s.trips, id)
			if k, ok := s.idemKeyOf[id]; ok {
				delete(s.idem, k)
				delete(s.idemKeyOf, id)
			}
		}
	}
	for id, q := range s.quotes {
		if now.Sub(q.ExpiresAt) > s.cfg.Retention {
			delete(s.quotes, id)
		}
	}
}

type Stats struct {
	Strategy    StrategyKind
	Index       IndexKind
	Drivers     map[DriverState]int
	Pending     int // trips waiting for a driver
	Active      int // trips with a driver on them that are not over
	TripsStored int
	SurgeMax    float64
	Geo         geo.Stats
	Dispatches  uint64
}

func (s *Service) Stats() Stats {
	now := s.clk.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Stats{Strategy: s.cfg.Strategy, Index: s.cfg.Index, Drivers: map[DriverState]int{}, TripsStored: len(s.trips),
		Geo: s.idx.Stats(), Dispatches: s.dispatches, SurgeMax: s.surge.Max(now)}
	for _, d := range s.drivers {
		st.Drivers[d.state]++
	}
	for _, t := range s.trips {
		switch {
		case t.State == trip.Requested:
			st.Pending++
		case !t.State.Terminal():
			st.Active++
		}
	}
	return st
}
