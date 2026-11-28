package ridesharing

import (
	"fmt"
	"time"

	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/geo"
	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/trip"
)

// Offer is what a driver is shown: a trip to take, and how long they have to say yes.
type Offer struct {
	TripID       string
	Pickup       geo.Point
	Dropoff      geo.Point
	PickupMeters float64 // from where the driver is now
	FareCents    int64
	ExpiresAt    time.Time
}

// UpdateLocation is the driver app's heartbeat. It places the driver on the map and, for a driver who was offline,
// brings them online and available.
func (s *Service) UpdateLocation(id string, p geo.Point) error {
	if id == "" {
		return fmt.Errorf("%w: driver id is required", ErrInvalidRequest)
	}
	if err := s.checkPoint("location", p); err != nil {
		return err
	}
	now := s.clk.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	// Under the lock, so a driver who goes offline cannot be put back on the map by a ping that was already
	// on its way.
	if err := s.idx.Upsert(id, p); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	d := s.drivers[id]
	if d == nil {
		d = &driver{id: id, state: DriverAvailable}
		s.drivers[id] = d
	} else if d.state == DriverOffline {
		d.state = DriverAvailable
	}
	d.lastSeen, d.pos = now, p
	return nil
}

// GoOffline takes a driver off the map. A driver with an offer out is treated as having declined it, and a driver
// on a trip cannot go offline until it is over.
func (s *Service) GoOffline(id string) error {
	now := s.clk.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.drivers[id]
	if d == nil || d.state == DriverOffline {
		return nil
	}
	switch d.state {
	case DriverBusy:
		return ErrDriverBusy
	case DriverReserved:
		s.declineLocked(d, now)
	}
	d.state, d.tripID, d.offerDeadline = DriverOffline, "", time.Time{}
	s.idx.Remove(id)
	return nil
}

// CurrentOffer is the offer waiting for a driver, if there is one. Drivers poll for it. A real service would push
// it to the phone, which is faster and cheaper, and polling keeps this one simple.
func (s *Service) CurrentOffer(driverID string) (Offer, bool) {
	now := s.clk.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.drivers[driverID]
	if d == nil || d.state != DriverReserved || !now.Before(d.offerDeadline) {
		return Offer{}, false
	}
	t := s.trips[d.tripID]
	o := Offer{TripID: t.ID, Pickup: t.Pickup, Dropoff: t.Dropoff, FareCents: t.FareCents, ExpiresAt: d.offerDeadline}
	if p, ok := s.idx.Get(driverID); ok {
		o.PickupMeters = geo.Distance(p, t.Pickup)
	}
	return o, true
}

// withDriverTrip finds the driver and the trip, and returns them under the lock.
func (s *Service) lookupLocked(driverID, tripID string) (*driver, trip.Trip, error) {
	d := s.drivers[driverID]
	if d == nil {
		return nil, trip.Trip{}, ErrDriverNotFound
	}
	t, ok := s.trips[tripID]
	if !ok {
		return nil, trip.Trip{}, ErrTripNotFound
	}
	return d, t, nil
}

// AcceptOffer takes the trip. It fails if the offer lapsed first, and the trip may by then have gone to someone else.
func (s *Service) AcceptOffer(driverID, tripID string) (trip.Trip, error) {
	now := s.clk.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	d, t, err := s.lookupLocked(driverID, tripID)
	if err != nil {
		return trip.Trip{}, err
	}
	if t.State == trip.Offered && t.Driver == driverID && !now.Before(t.OfferDeadline) {
		s.expireOfferLocked(d, now)
		return s.trips[tripID], ErrOfferExpired
	}
	next, changed, err := t.Apply(trip.Event{Kind: trip.EvAccept, At: now, Driver: driverID})
	if err != nil {
		return t, conflict(err)
	}
	if changed {
		s.trips[tripID] = next
		d.state, d.tripID, d.offerDeadline = DriverBusy, tripID, time.Time{}
		s.obs.OfferResult("accepted")
		s.obs.TripMatched(now.Sub(t.CreatedAt))
	}
	return next, nil
}

// DeclineOffer turns the trip down. It goes back to waiting, and this driver is not asked about it again.
func (s *Service) DeclineOffer(driverID, tripID string) (trip.Trip, error) {
	now := s.clk.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	d, t, err := s.lookupLocked(driverID, tripID)
	if err != nil {
		return trip.Trip{}, err
	}
	next, changed, err := t.Apply(trip.Event{Kind: trip.EvDecline, At: now, Driver: driverID})
	if err != nil {
		return t, conflict(err)
	}
	if changed {
		s.commitLocked(next, now)
		s.freeDriverLocked(d)
		s.obs.OfferResult("declined")
		if s.cfg.Strategy == StrategyGreedy {
			s.dispatchLocked(now)
		}
	}
	return s.trips[tripID], nil
}

// declineLocked is a decline on the driver's behalf, for a driver who goes offline with an offer out.
func (s *Service) declineLocked(d *driver, now time.Time) {
	t, ok := s.trips[d.tripID]
	if !ok {
		return
	}
	if next, changed, err := t.Apply(trip.Event{Kind: trip.EvDecline, At: now, Driver: d.id}); err == nil && changed {
		s.commitLocked(next, now)
		s.obs.OfferResult("declined")
		if s.cfg.Strategy == StrategyGreedy {
			defer s.dispatchLocked(now)
		}
	}
}

func (s *Service) freeDriverLocked(d *driver) {
	d.state, d.tripID, d.offerDeadline = DriverAvailable, "", time.Time{}
}

// progress applies a driver's step along the trip: arriving, starting, completing.
func (s *Service) progress(driverID, tripID string, kind trip.Kind) (trip.Trip, error) {
	now := s.clk.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	d, t, err := s.lookupLocked(driverID, tripID)
	if err != nil {
		return trip.Trip{}, err
	}
	next, changed, err := t.Apply(trip.Event{Kind: kind, At: now, Driver: driverID})
	if err != nil {
		return t, conflict(err)
	}
	if changed {
		s.commitLocked(next, now)
		if kind == trip.EvComplete {
			s.freeDriverLocked(d)
		}
	}
	return next, nil
}

func (s *Service) ArriveAtPickup(driverID, tripID string) (trip.Trip, error) {
	return s.progress(driverID, tripID, trip.EvArrive)
}

func (s *Service) StartTrip(driverID, tripID string) (trip.Trip, error) {
	return s.progress(driverID, tripID, trip.EvStart)
}

func (s *Service) CompleteTrip(driverID, tripID string) (trip.Trip, error) {
	return s.progress(driverID, tripID, trip.EvComplete)
}

// DriverCancel is a driver backing out after accepting. The rider goes back to waiting, ahead of newer requests
// because they have already waited, and this driver is not offered the trip again.
func (s *Service) DriverCancel(driverID, tripID string) (trip.Trip, error) {
	now := s.clk.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	d, t, err := s.lookupLocked(driverID, tripID)
	if err != nil {
		return trip.Trip{}, err
	}
	next, changed, err := t.Apply(trip.Event{Kind: trip.EvDriverCancel, At: now, Driver: driverID})
	if err != nil {
		return t, conflict(err)
	}
	if changed {
		s.commitLocked(next, now)
		s.freeDriverLocked(d)
		if s.cfg.Strategy == StrategyGreedy {
			s.dispatchLocked(now)
		}
	}
	return s.trips[tripID], nil
}
