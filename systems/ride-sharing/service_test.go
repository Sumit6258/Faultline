package ridesharing

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/clock"
	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/dispatch"
	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/geo"
	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/pricing"
	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/trip"
)

var start = time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)

func pt(lat, lon float64) geo.Point { return geo.Point{Lat: lat, Lon: lon} }

// about 111 m per 0.001 degree of latitude
var (
	center  = pt(12.9700, 77.5900)
	dropoff = pt(12.9900, 77.6100)
)

type rec struct {
	mu                          sync.Mutex
	requested                   int
	offered, accepted, declined int
	expired                     int
	matched                     []time.Duration
	ended                       map[trip.State]int
}

func (r *rec) TripRequested() { r.mu.Lock(); r.requested++; r.mu.Unlock() }
func (r *rec) OfferResult(o string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch o {
	case "offered":
		r.offered++
	case "accepted":
		r.accepted++
	case "declined":
		r.declined++
	case "expired":
		r.expired++
	}
}
func (r *rec) TripMatched(d time.Duration) {
	r.mu.Lock()
	r.matched = append(r.matched, d)
	r.mu.Unlock()
}
func (r *rec) TripEnded(s trip.State) {
	r.mu.Lock()
	if r.ended == nil {
		r.ended = map[trip.State]int{}
	}
	r.ended[s]++
	r.mu.Unlock()
}

func newSvc(t *testing.T, cfg Config) (*Service, *clock.Fake, *rec) {
	t.Helper()
	clk := clock.NewFake(start)
	cfg.Clock = clk
	r := &rec{}
	cfg.Observer = r
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s, clk, r
}

func online(t *testing.T, s *Service, id string, p geo.Point) {
	t.Helper()
	if err := s.UpdateLocation(id, p); err != nil {
		t.Fatal(err)
	}
}

func request(t *testing.T, s *Service, rider string, from geo.Point) trip.Trip {
	t.Helper()
	tr, _, err := s.RequestTrip(TripRequest{Rider: rider, From: from, To: dropoff})
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func get(t *testing.T, s *Service, id string) trip.Trip {
	t.Helper()
	v, err := s.GetTrip(id)
	if err != nil {
		t.Fatal(err)
	}
	return v.Trip
}

func TestARideFromRequestToCompletion(t *testing.T) {
	s, clk, r := newSvc(t, Config{})
	online(t, s, "d1", pt(12.9710, 77.5900))
	tr := request(t, s, "alice", center)
	if tr.State != trip.Offered || tr.Driver != "d1" {
		t.Fatalf("with a driver nearby the trip should be offered at once: %+v", tr)
	}
	offer, ok := s.CurrentOffer("d1")
	if !ok || offer.TripID != tr.ID || offer.PickupMeters < 100 || offer.PickupMeters > 120 || offer.FareCents != tr.FareCents {
		t.Fatalf("offer %+v %v", offer, ok)
	}
	clk.Advance(4 * time.Second)
	for _, step := range []struct {
		name string
		do   func() (trip.Trip, error)
		want trip.State
	}{
		{"accept", func() (trip.Trip, error) { return s.AcceptOffer("d1", tr.ID) }, trip.Accepted},
		{"arrive", func() (trip.Trip, error) { return s.ArriveAtPickup("d1", tr.ID) }, trip.Arrived},
		{"start", func() (trip.Trip, error) { return s.StartTrip("d1", tr.ID) }, trip.InProgress},
		{"complete", func() (trip.Trip, error) { return s.CompleteTrip("d1", tr.ID) }, trip.Completed},
	} {
		got, err := step.do()
		if err != nil || got.State != step.want {
			t.Fatalf("%s: %v %+v", step.name, err, got)
		}
	}
	if _, ok := s.CurrentOffer("d1"); ok {
		t.Error("a finished trip leaves no offer")
	}
	if st := s.Stats(); st.Drivers[DriverAvailable] != 1 || st.Pending != 0 || st.Active != 0 {
		t.Errorf("the driver should be free again: %+v", st)
	}
	if r.requested != 1 || r.offered != 1 || r.accepted != 1 || r.ended[trip.Completed] != 1 || len(r.matched) != 1 || r.matched[0] != 4*time.Second {
		t.Errorf("observer: %+v", r)
	}
	// Once a rider's trip has ended they can ask for another.
	if _, _, err := s.RequestTrip(TripRequest{Rider: "alice", From: center, To: dropoff}); err != nil {
		t.Errorf("%v", err)
	}
}

func TestWithNoDriversTheTripWaitsThenGivesUp(t *testing.T) {
	s, clk, r := newSvc(t, Config{})
	tr := request(t, s, "alice", center)
	if tr.State != trip.Requested {
		t.Fatalf("%+v", tr)
	}
	clk.Advance(119 * time.Second)
	s.Tick()
	if get(t, s, tr.ID).State != trip.Requested {
		t.Fatal("it should still be waiting just before the limit")
	}
	clk.Advance(2 * time.Second)
	s.Tick()
	if got := get(t, s, tr.ID); got.State != trip.NoDrivers {
		t.Fatalf("%+v", got)
	}
	if r.ended[trip.NoDrivers] != 1 {
		t.Errorf("%+v", r.ended)
	}
	// A driver arriving later finds nothing waiting.
	online(t, s, "d1", center)
	s.Tick()
	if _, ok := s.CurrentOffer("d1"); ok {
		t.Error("nothing should be offered for a trip that was given up on")
	}
}

func TestADriverArrivingWhileATripWaitsIsOfferedIt(t *testing.T) {
	s, clk, _ := newSvc(t, Config{})
	tr := request(t, s, "alice", center)
	clk.Advance(20 * time.Second)
	online(t, s, "d1", pt(12.9720, 77.5900))
	s.Tick()
	if got := get(t, s, tr.ID); got.State != trip.Offered || got.Driver != "d1" {
		t.Errorf("%+v", got)
	}
}

func TestADriverTooFarAwayIsNotOffered(t *testing.T) {
	s, _, _ := newSvc(t, Config{})
	online(t, s, "far", pt(13.05, 77.59)) // about 9 km
	if tr := request(t, s, "alice", center); tr.State != trip.Requested {
		t.Errorf("%+v", tr)
	}
}

func TestAnOfferThatIsNotAnsweredLapsesAndGoesToTheNextDriver(t *testing.T) {
	s, clk, r := newSvc(t, Config{})
	online(t, s, "d1", pt(12.9710, 77.5900))
	online(t, s, "d2", pt(12.9750, 77.5900))
	tr := request(t, s, "alice", center)
	if tr.Driver != "d1" {
		t.Fatalf("the nearer driver first: %+v", tr)
	}
	clk.Advance(9 * time.Second)
	s.Tick()
	if get(t, s, tr.ID).Driver != "d1" {
		t.Fatal("the offer is still good at nine seconds")
	}
	clk.Advance(1 * time.Second)
	s.Tick()
	got := get(t, s, tr.ID)
	if got.State != trip.Offered || got.Driver != "d2" || !got.WasAttempted("d1") {
		t.Fatalf("after the offer lapsed: %+v", got)
	}
	if r.expired != 1 || r.offered != 2 {
		t.Errorf("%+v", r)
	}
	// d1 is free again, but is not offered a trip it let lapse.
	if _, ok := s.CurrentOffer("d1"); ok {
		t.Error("d1 should have no offer")
	}
	if st := s.Stats(); st.Drivers[DriverAvailable] != 1 || st.Drivers[DriverReserved] != 1 {
		t.Errorf("%+v", st.Drivers)
	}
}

func TestAcceptingAfterTheOfferLapsedFails(t *testing.T) {
	s, clk, _ := newSvc(t, Config{})
	online(t, s, "d1", pt(12.9710, 77.5900))
	online(t, s, "d2", pt(12.9750, 77.5900))
	tr := request(t, s, "alice", center)
	clk.Advance(10 * time.Second) // exactly the deadline
	if _, err := s.AcceptOffer("d1", tr.ID); !errors.Is(err, ErrOfferExpired) {
		t.Fatalf("%v", err)
	}
	// The trip has been given to d2 meanwhile, and d1 cannot take it back.
	s.Tick()
	if got := get(t, s, tr.ID); got.Driver != "d2" || got.State != trip.Offered {
		t.Fatalf("%+v", got)
	}
	if _, err := s.AcceptOffer("d1", tr.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("a driver whose offer was reassigned: %v", err)
	}
	if _, err := s.AcceptOffer("d2", tr.ID); err != nil {
		t.Errorf("%v", err)
	}
}

func TestAcceptingJustBeforeTheDeadlineWorks(t *testing.T) {
	s, clk, _ := newSvc(t, Config{})
	online(t, s, "d1", pt(12.9710, 77.5900))
	tr := request(t, s, "alice", center)
	clk.Advance(10*time.Second - time.Millisecond)
	if got, err := s.AcceptOffer("d1", tr.ID); err != nil || got.State != trip.Accepted {
		t.Errorf("%v %+v", err, got)
	}
}

func TestDecliningPassesTheTripOnAndNeverBackToTheSameDriver(t *testing.T) {
	s, _, r := newSvc(t, Config{})
	online(t, s, "d1", pt(12.9710, 77.5900))
	tr := request(t, s, "alice", center)
	got, err := s.DeclineOffer("d1", tr.ID)
	if err != nil || got.State != trip.Requested || !got.WasAttempted("d1") {
		t.Fatalf("%v %+v", err, got)
	}
	s.Tick()
	if get(t, s, tr.ID).State != trip.Requested {
		t.Error("the only driver turned it down, so it should wait, not be offered to them again")
	}
	online(t, s, "d2", pt(12.9760, 77.5900))
	s.Tick()
	if got := get(t, s, tr.ID); got.Driver != "d2" {
		t.Errorf("%+v", got)
	}
	if r.declined != 1 {
		t.Errorf("%+v", r)
	}
}

func TestRepeatedDriverActionsAreHarmless(t *testing.T) {
	s, _, r := newSvc(t, Config{})
	online(t, s, "d1", pt(12.9710, 77.5900))
	tr := request(t, s, "alice", center)
	a, _ := s.AcceptOffer("d1", tr.ID)
	b, err := s.AcceptOffer("d1", tr.ID)
	if err != nil || b.Version != a.Version || r.accepted != 1 || len(r.matched) != 1 {
		t.Errorf("a retried accept: %v version %d to %d, accepted %d times", err, a.Version, b.Version, r.accepted)
	}
	s.ArriveAtPickup("d1", tr.ID)
	if c, err := s.ArriveAtPickup("d1", tr.ID); err != nil || c.State != trip.Arrived {
		t.Errorf("%v %+v", err, c)
	}
}

func TestOnlyTheAssignedDriverCanActOnATrip(t *testing.T) {
	s, _, _ := newSvc(t, Config{})
	online(t, s, "d1", pt(12.9710, 77.5900))
	online(t, s, "d2", pt(12.9900, 77.5900))
	tr := request(t, s, "alice", center)
	for name, err := range map[string]error{
		"accept":  func() error { _, e := s.AcceptOffer("d2", tr.ID); return e }(),
		"decline": func() error { _, e := s.DeclineOffer("d2", tr.ID); return e }(),
	} {
		if !errors.Is(err, ErrConflict) {
			t.Errorf("%s by a driver who was not offered the trip: %v", name, err)
		}
	}
	s.AcceptOffer("d1", tr.ID)
	for name, err := range map[string]error{
		"arrive":   func() error { _, e := s.ArriveAtPickup("d2", tr.ID); return e }(),
		"complete": func() error { _, e := s.CompleteTrip("d2", tr.ID); return e }(),
	} {
		if !errors.Is(err, ErrConflict) {
			t.Errorf("%s by another driver: %v", name, err)
		}
	}
	if _, err := s.AcceptOffer("nobody", tr.ID); !errors.Is(err, ErrDriverNotFound) {
		t.Errorf("%v", err)
	}
	if _, err := s.AcceptOffer("d1", "trip-999999"); !errors.Is(err, ErrTripNotFound) {
		t.Errorf("%v", err)
	}
	if _, err := s.StartTrip("d1", tr.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("starting before arriving: %v", err)
	}
}

func TestARiderCancellingBeforeAcceptanceOwesNothingAndFreesTheDriver(t *testing.T) {
	s, _, r := newSvc(t, Config{})
	online(t, s, "d1", pt(12.9710, 77.5900))
	tr := request(t, s, "alice", center)
	got, err := s.CancelTrip(tr.ID, "rider")
	if err != nil || got.State != trip.Cancelled || got.CancelFeeCents != 0 {
		t.Fatalf("%v %+v", err, got)
	}
	if _, ok := s.CurrentOffer("d1"); ok {
		t.Error("the offer should be withdrawn")
	}
	if st := s.Stats(); st.Drivers[DriverAvailable] != 1 {
		t.Errorf("%+v", st.Drivers)
	}
	if r.ended[trip.Cancelled] != 1 {
		t.Errorf("%+v", r.ended)
	}
	if again, err := s.CancelTrip(tr.ID, "rider"); err != nil || again.Version != got.Version {
		t.Errorf("cancelling twice: %v", err)
	}
}

func TestARiderCancellingAfterAcceptanceIsChargedTheFeeAndTheDriverIsFreed(t *testing.T) {
	s, _, _ := newSvc(t, Config{})
	online(t, s, "d1", pt(12.9710, 77.5900))
	tr := request(t, s, "alice", center)
	s.AcceptOffer("d1", tr.ID)
	got, err := s.CancelTrip(tr.ID, "rider")
	if err != nil || got.CancelFeeCents != 500 {
		t.Fatalf("%v %+v", err, got)
	}
	if st := s.Stats(); st.Drivers[DriverAvailable] != 1 || st.Drivers[DriverBusy] != 0 {
		t.Errorf("%+v", st.Drivers)
	}
	online(t, s, "d1", pt(12.9710, 77.5900))
	if next := request(t, s, "bob", center); next.Driver != "d1" {
		t.Errorf("the freed driver should be offered the next trip: %+v", next)
	}
	sys, _ := s.CancelTrip(tr.ID, "system")
	if sys.CancelFeeCents != 500 {
		t.Error("a later system cancel of an already cancelled trip must not change it")
	}
	if _, err := s.CancelTrip(tr.ID, "someone"); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("%v", err)
	}
}

func TestARideInProgressCannotBeCancelled(t *testing.T) {
	s, _, _ := newSvc(t, Config{})
	online(t, s, "d1", pt(12.9710, 77.5900))
	tr := request(t, s, "alice", center)
	s.AcceptOffer("d1", tr.ID)
	s.ArriveAtPickup("d1", tr.ID)
	s.StartTrip("d1", tr.ID)
	if _, err := s.CancelTrip(tr.ID, "rider"); !errors.Is(err, ErrConflict) {
		t.Errorf("%v", err)
	}
}

func TestADriverBackingOutSendsTheRiderToTheNextDriver(t *testing.T) {
	s, _, _ := newSvc(t, Config{})
	online(t, s, "d1", pt(12.9710, 77.5900))
	online(t, s, "d2", pt(12.9760, 77.5900))
	tr := request(t, s, "alice", center)
	s.AcceptOffer("d1", tr.ID)
	got, err := s.DriverCancel("d1", tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != trip.Offered || got.Driver != "d2" || !got.WasAttempted("d1") {
		t.Errorf("%+v", got)
	}
	if st := s.Stats(); st.Drivers[DriverAvailable] != 1 {
		t.Errorf("d1 should be free: %+v", st.Drivers)
	}
}

func TestSilentDriversAreDroppedFromTheMap(t *testing.T) {
	s, clk, _ := newSvc(t, Config{})
	online(t, s, "quiet", center)
	online(t, s, "chatty", pt(12.975, 77.59))
	clk.Advance(20 * time.Second)
	online(t, s, "chatty", pt(12.975, 77.59))
	clk.Advance(15 * time.Second) // quiet last spoke 35 s ago, chatty 15 s ago
	s.Tick()
	st := s.Stats()
	if st.Drivers[DriverOffline] != 1 || st.Drivers[DriverAvailable] != 1 {
		t.Errorf("%+v", st.Drivers)
	}
	if request := request(t, s, "alice", center); request.Driver != "chatty" {
		t.Errorf("the silent driver must not be offered trips: %+v", request)
	}
	online(t, s, "quiet", center) // and a ping brings them back
	if st := s.Stats(); st.Drivers[DriverOffline] != 0 || st.Drivers[DriverAvailable]+st.Drivers[DriverReserved] != 2 {
		t.Errorf("%+v", st.Drivers)
	}
}

func TestADriverWhoGoesSilentWithAnOfferOutLosesTheOffer(t *testing.T) {
	s, clk, _ := newSvc(t, Config{OfferTTL: time.Minute})
	online(t, s, "d1", pt(12.9710, 77.5900))
	tr := request(t, s, "alice", center)
	clk.Advance(31 * time.Second)
	online(t, s, "d2", pt(12.9760, 77.5900))
	s.Tick()
	if got := get(t, s, tr.ID); got.Driver != "d2" || !got.WasAttempted("d1") {
		t.Errorf("%+v", got)
	}
}

func TestABusyDriverStaysBusyEvenIfTheirPingsStop(t *testing.T) {
	s, clk, _ := newSvc(t, Config{})
	online(t, s, "d1", pt(12.9710, 77.5900))
	tr := request(t, s, "alice", center)
	s.AcceptOffer("d1", tr.ID)
	clk.Advance(5 * time.Minute)
	s.Tick()
	if st := s.Stats(); st.Drivers[DriverBusy] != 1 {
		t.Errorf("a driver mid trip with a tunnel in the way is still on the trip: %+v", st.Drivers)
	}
}

func TestGoingOffline(t *testing.T) {
	s, _, r := newSvc(t, Config{})
	online(t, s, "free", pt(12.9900, 77.5900))
	if err := s.GoOffline("free"); err != nil {
		t.Fatal(err)
	}
	waiting := request(t, s, "a", center)
	if waiting.State != trip.Requested {
		t.Error("an offline driver must not be offered trips")
	}
	if err := s.GoOffline("never-seen"); err != nil {
		t.Errorf("going offline twice or for someone unknown is harmless: %v", err)
	}
	online(t, s, "d1", pt(12.9710, 77.5900))
	s.Tick() // d1 is offered rider a's trip
	if st := s.Stats(); st.Drivers[DriverReserved] != 1 {
		t.Fatalf("%+v", st.Drivers)
	}
	if err := s.GoOffline("d1"); err != nil {
		t.Fatal(err)
	}
	if r.declined != 1 {
		t.Errorf("going offline with an offer out is a decline: %+v", r)
	}
	s.CancelTrip(waiting.ID, "rider") // so the new driver is not offered the older trip first
	online(t, s, "d2", pt(12.9720, 77.5900))
	tr2 := request(t, s, "b", pt(12.9720, 77.5910))
	s.AcceptOffer("d2", tr2.ID)
	if err := s.GoOffline("d2"); !errors.Is(err, ErrDriverBusy) {
		t.Errorf("a driver on a trip cannot go offline: %v", err)
	}
}

func TestRequestsAndLocationsAreValidated(t *testing.T) {
	s, _, _ := newSvc(t, Config{})
	bad := []struct {
		name string
		err  error
	}{
		{"no rider", func() error { _, _, e := s.RequestTrip(TripRequest{From: center, To: dropoff}); return e }()},
		{"pickup outside the area", func() error {
			_, _, e := s.RequestTrip(TripRequest{Rider: "a", From: pt(40, 77.5), To: dropoff})
			return e
		}()},
		{"dropoff outside the area", func() error {
			_, _, e := s.RequestTrip(TripRequest{Rider: "a", From: center, To: pt(12.9, 80)})
			return e
		}()},
		{"nonsense coordinates", func() error {
			_, _, e := s.RequestTrip(TripRequest{Rider: "a", From: pt(200, 0), To: dropoff})
			return e
		}()},
		{"pickup is dropoff", func() error { _, _, e := s.RequestTrip(TripRequest{Rider: "a", From: center, To: center}); return e }()},
		{"driver location outside", s.UpdateLocation("d", pt(40, 77.5))},
		{"driver without an id", s.UpdateLocation("", center)},
		{"quote without a rider", func() error { _, e := s.Quote("", center, dropoff); return e }()},
	}
	for _, b := range bad {
		if !errors.Is(b.err, ErrInvalidRequest) {
			t.Errorf("%s: %v", b.name, b.err)
		}
	}
	if st := s.Stats(); st.TripsStored != 0 || st.Drivers[DriverAvailable] != 0 {
		t.Errorf("rejected input must leave nothing behind: %+v", st)
	}
}

func TestARiderCannotHaveTwoTripsAtOnce(t *testing.T) {
	s, _, _ := newSvc(t, Config{})
	first := request(t, s, "alice", center)
	if _, _, err := s.RequestTrip(TripRequest{Rider: "alice", From: pt(12.98, 77.6), To: dropoff}); !errors.Is(err, ErrRiderBusy) {
		t.Fatalf("%v", err)
	}
	s.CancelTrip(first.ID, "rider")
	if _, _, err := s.RequestTrip(TripRequest{Rider: "alice", From: center, To: dropoff}); err != nil {
		t.Errorf("after cancelling %v", err)
	}
}

func TestARetriedRequestWithTheSameKeyReturnsTheSameTrip(t *testing.T) {
	s, _, r := newSvc(t, Config{})
	req := TripRequest{Rider: "alice", From: center, To: dropoff, IdempotencyKey: "k1"}
	first, created, err := s.RequestTrip(req)
	if err != nil || !created {
		t.Fatalf("%v %v", err, created)
	}
	again, created, err := s.RequestTrip(req)
	if err != nil || created || again.ID != first.ID {
		t.Errorf("a retry must return the original trip: %v created=%v %s vs %s", err, created, again.ID, first.ID)
	}
	if r.requested != 1 {
		t.Errorf("a retry must not count as a second request: %d", r.requested)
	}
	other := req
	other.To = pt(12.99, 77.62)
	if _, _, err := s.RequestTrip(other); !errors.Is(err, ErrIdempotencyConflict) {
		t.Errorf("the same key with a different trip: %v", err)
	}
	// The same key from a different rider is a different request.
	if _, created, err := s.RequestTrip(TripRequest{Rider: "bob", From: center, To: dropoff, IdempotencyKey: "k1"}); err != nil || !created {
		t.Errorf("%v %v", err, created)
	}
}

func TestAnIdempotentRetryStillWorksAfterTheTripHasMovedOn(t *testing.T) {
	s, _, _ := newSvc(t, Config{})
	online(t, s, "d1", pt(12.9710, 77.5900))
	req := TripRequest{Rider: "alice", From: center, To: dropoff, IdempotencyKey: "k1"}
	first, _, _ := s.RequestTrip(req)
	s.AcceptOffer("d1", first.ID)
	again, created, err := s.RequestTrip(req)
	if err != nil || created || again.State != trip.Accepted {
		t.Errorf("a retry sees the trip as it is now, not as it was: %v %+v", err, again)
	}
}

func TestAQuoteLocksThePriceEvenWhenSurgeRises(t *testing.T) {
	s, clk, _ := newSvc(t, Config{})
	q, err := s.Quote("alice", center, dropoff)
	if err != nil || q.Multiplier != 1 || q.FareCents <= 0 {
		t.Fatalf("%v %+v", err, q)
	}
	// A wave of demand with one driver in the neighbourhood.
	online(t, s, "d1", pt(12.9710, 77.5900))
	for i := 0; i < 40; i++ {
		s.surge.RecordRequest(s.cells.Label(center), clk.Now())
	}
	clk.Advance(2 * time.Minute)
	s.Tick()
	fresh, _ := s.Quote("bob", center, dropoff)
	if fresh.Multiplier <= 1 || fresh.FareCents <= q.FareCents {
		t.Fatalf("setup: surge should have risen: %+v", fresh)
	}
	tr, _, err := s.RequestTrip(TripRequest{Rider: "alice", From: center, To: dropoff, QuoteID: q.ID})
	if err != nil || tr.FareCents != q.FareCents || tr.Multiplier != 1 || tr.QuoteID != q.ID {
		t.Errorf("alice must pay the price she was quoted: %v %+v", err, tr)
	}
	unquoted, _, _ := s.RequestTrip(TripRequest{Rider: "carol", From: center, To: dropoff})
	if unquoted.Multiplier <= 1 {
		t.Errorf("a rider without a quote pays the current price: %+v", unquoted)
	}
}

func TestQuotesExpireAreSingleUseAndBelongToOneRiderAndRoute(t *testing.T) {
	s, clk, _ := newSvc(t, Config{})
	q, _ := s.Quote("alice", center, dropoff)
	if _, _, err := s.RequestTrip(TripRequest{Rider: "bob", From: center, To: dropoff, QuoteID: q.ID}); !errors.Is(err, ErrQuoteNotFound) {
		t.Errorf("someone else's quote: %v", err)
	}
	if _, _, err := s.RequestTrip(TripRequest{Rider: "alice", From: center, To: pt(12.98, 77.6), QuoteID: q.ID}); !errors.Is(err, ErrQuoteMismatch) {
		t.Errorf("a quote for a different route: %v", err)
	}
	if _, _, err := s.RequestTrip(TripRequest{Rider: "alice", From: center, To: dropoff, QuoteID: "quote-000000"}); !errors.Is(err, ErrQuoteNotFound) {
		t.Errorf("%v", err)
	}
	tr, _, err := s.RequestTrip(TripRequest{Rider: "alice", From: center, To: dropoff, QuoteID: q.ID})
	if err != nil {
		t.Fatal(err)
	}
	s.CancelTrip(tr.ID, "rider")
	if _, _, err := s.RequestTrip(TripRequest{Rider: "alice", From: center, To: dropoff, QuoteID: q.ID}); !errors.Is(err, ErrQuoteUsed) {
		t.Errorf("a quote can only be used once: %v", err)
	}
	old, _ := s.Quote("alice", center, dropoff)
	clk.Advance(2*time.Minute + time.Second)
	if _, _, err := s.RequestTrip(TripRequest{Rider: "alice", From: center, To: dropoff, QuoteID: old.ID}); !errors.Is(err, ErrQuoteExpired) {
		t.Errorf("%v", err)
	}
}

func TestFinishedTripsAreForgottenAfterTheRetentionPeriod(t *testing.T) {
	s, clk, _ := newSvc(t, Config{})
	online(t, s, "d1", pt(12.9710, 77.5900))
	done := request(t, s, "alice", center)
	s.AcceptOffer("d1", done.ID)
	s.ArriveAtPickup("d1", done.ID)
	s.StartTrip("d1", done.ID)
	s.CompleteTrip("d1", done.ID)
	live := request(t, s, "bob", center)
	s.AcceptOffer("d1", live.ID)
	clk.Advance(11 * time.Minute)
	online(t, s, "d1", pt(12.9710, 77.5900))
	s.Tick()
	if _, err := s.GetTrip(done.ID); !errors.Is(err, ErrTripNotFound) {
		t.Errorf("a finished trip is pruned after ten minutes: %v", err)
	}
	if _, err := s.GetTrip(live.ID); err != nil {
		t.Errorf("a live trip must never be pruned: %v", err)
	}
}

func TestARiderSeesWhereTheirDriverIs(t *testing.T) {
	s, _, _ := newSvc(t, Config{})
	online(t, s, "d1", pt(12.9710, 77.5900))
	tr := request(t, s, "alice", center)
	v, _ := s.GetTrip(tr.ID)
	if v.DriverLocation == nil || *v.DriverLocation != pt(12.9710, 77.5900) {
		t.Fatalf("%+v", v)
	}
	online(t, s, "d1", pt(12.9705, 77.5900))
	if v, _ = s.GetTrip(tr.ID); v.DriverLocation == nil || v.DriverLocation.Lat != 12.9705 {
		t.Errorf("the driver moved: %+v", v.DriverLocation)
	}
	if _, err := s.GetTrip("trip-nope"); !errors.Is(err, ErrTripNotFound) {
		t.Errorf("%v", err)
	}
}

// ---------------------------------------------------------------- strategies

// The scenario from the dispatch tests, through the whole service. Rider A is nearer driver 1, but driver 1 is
// the only one anywhere near rider B.
func scenario(t *testing.T, kind StrategyKind) (aDriver, bDriver string) {
	s, clk, _ := newSvc(t, Config{Strategy: kind})
	online(t, s, "d1", pt(12.9700, 77.5900))
	online(t, s, "d2", pt(12.9745, 77.5900))
	a, _, err := s.RequestTrip(TripRequest{Rider: "A", From: pt(12.9718, 77.5900), To: dropoff})
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(500 * time.Millisecond)
	b, _, _ := s.RequestTrip(TripRequest{Rider: "B", From: pt(12.9680, 77.5900), To: dropoff})
	s.Tick()
	return get(t, s, a.ID).Driver, get(t, s, b.ID).Driver
}

func TestGreedyServesRidersAsTheyArriveAndBatchedPairsThemTogether(t *testing.T) {
	a, b := scenario(t, StrategyGreedy)
	if a != "d1" || b != "d2" {
		t.Errorf("greedy: A got %q and B got %q, expected the nearest driver for A and what is left for B", a, b)
	}
	a, b = scenario(t, StrategyBatched)
	if a != "d2" || b != "d1" {
		t.Errorf("batched: A got %q and B got %q, expected the pairing that makes the total pickup shortest", a, b)
	}
}

func TestBatchedWaitsForItsWindow(t *testing.T) {
	s, clk, _ := newSvc(t, Config{Strategy: StrategyBatched})
	online(t, s, "d1", pt(12.9710, 77.5900))
	first := request(t, s, "alice", center)
	if first.State != trip.Requested {
		t.Fatalf("batched must not act on a request until its window closes: %+v", first)
	}
	s.Tick() // the first tick always runs
	if get(t, s, first.ID).State != trip.Offered {
		t.Fatal("the first tick should have matched it")
	}
	s.AcceptOffer("d1", first.ID)
	s.CompleteTripForTest(t, "d1", first.ID)
	online(t, s, "d2", pt(12.9720, 77.5900))
	clk.Advance(500 * time.Millisecond)
	second := request(t, s, "bob", center)
	clk.Advance(500 * time.Millisecond)
	s.Tick()
	if get(t, s, second.ID).State != trip.Requested {
		t.Error("one second after the last batch is inside the two second window")
	}
	clk.Advance(1 * time.Second)
	s.Tick()
	if get(t, s, second.ID).State != trip.Offered {
		t.Error("the window has closed, so the trip should be matched")
	}
}

// CompleteTripForTest runs a trip from accepted to completed.
func (s *Service) CompleteTripForTest(t *testing.T, driverID, tripID string) {
	t.Helper()
	for _, f := range []func(string, string) (trip.Trip, error){s.ArriveAtPickup, s.StartTrip, s.CompleteTrip} {
		if _, err := f(driverID, tripID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTheGridAndBruteForceIndexesGiveTheSameDispatches(t *testing.T) {
	run := func(index IndexKind) string {
		s, clk, _ := newSvc(t, Config{Index: index, Strategy: StrategyBatched})
		for i := 0; i < 60; i++ {
			online(t, s, fmt.Sprintf("d%02d", i), pt(12.93+float64(i%10)*0.012, 77.52+float64(i/10)*0.02))
		}
		var ids []string
		for i := 0; i < 40; i++ {
			tr, _, err := s.RequestTrip(TripRequest{Rider: fmt.Sprintf("r%02d", i), From: pt(12.93+float64(i%8)*0.014, 77.53+float64(i/8)*0.025), To: dropoff})
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, tr.ID)
		}
		clk.Advance(time.Second)
		s.Tick()
		out := ""
		for _, id := range ids {
			out += fmt.Sprintf("%s:%s:%s ", id, get(t, s, id).State, get(t, s, id).Driver)
		}
		return out
	}
	if g, b := run(IndexGrid), run(IndexBrute); g != b {
		t.Errorf("the index must not change who gets whom:\ngrid  %s\nbrute %s", g, b)
	}
}

func TestConfigIsValidated(t *testing.T) {
	for name, cfg := range map[string]Config{
		"unknown strategy": {Strategy: "random"},
		"unknown index":    {Index: "kdtree"},
		"bad bounds":       {Bounds: geo.Bounds{MinLat: 10, MaxLat: 5, MinLon: 1, MaxLon: 2}},
		"bad surge":        {Surge: pricing.SurgeConfig{Window: time.Minute, Interval: time.Second, Capacity: 1, Slope: 1, Step: 1, Grain: 0.1, Cap: 0.5}},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("%s should be refused", name)
		}
	}
	if _, err := New(Config{}); err != nil {
		t.Errorf("the defaults should work: %v", err)
	}
}

// The strategy only advises. If one ever proposed the same driver for two trips, or a driver who is no longer
// free, the service must still refuse to double assign, because that check is what the correctness rests on.
type doubleBooker struct{}

func (doubleBooker) Name() string { return "double-booker" }
func (doubleBooker) Propose(reqs []dispatch.Request, ix geo.Index, available func(string) bool, o dispatch.Options) []dispatch.Pair {
	var out []dispatch.Pair
	for _, r := range reqs {
		out = append(out, dispatch.Pair{TripID: r.TripID, DriverID: "d1"}, dispatch.Pair{TripID: r.TripID, DriverID: "busy"})
	}
	return out
}

func TestAMisbehavingStrategyCannotDoubleAssignADriver(t *testing.T) {
	s, _, _ := newSvc(t, Config{})
	s.strategy = doubleBooker{}
	online(t, s, "d1", pt(12.9710, 77.5900))
	online(t, s, "busy", pt(12.9712, 77.5900))
	first := request(t, s, "alice", center)
	s.AcceptOffer("d1", first.ID)
	s.mu.Lock()
	s.drivers["busy"].state = DriverBusy // someone already has this driver
	s.drivers["busy"].tripID = "trip-elsewhere"
	s.mu.Unlock()
	for i := 0; i < 5; i++ {
		request(t, s, fmt.Sprintf("rider%d", i), center)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	offeredTo := map[string]int{}
	for _, tr := range s.trips {
		if tr.State == trip.Offered {
			offeredTo[tr.Driver]++
		}
	}
	if len(offeredTo) != 0 {
		t.Errorf("d1 is busy and busy is busy, so nobody should have been offered anything: %v", offeredTo)
	}
}

func TestTheOldestWaitingTripGetsTheFirstDriverToBecomeFree(t *testing.T) {
	s, clk, _ := newSvc(t, Config{})
	older := request(t, s, "first", center)
	clk.Advance(30 * time.Second)
	newer := request(t, s, "second", center)
	online(t, s, "d1", pt(12.9710, 77.5900))
	s.Tick()
	if get(t, s, older.ID).Driver != "d1" || get(t, s, newer.ID).State != trip.Requested {
		t.Errorf("first come first served: older %+v, newer %+v", get(t, s, older.ID).State, get(t, s, newer.ID).State)
	}
}

func TestSupplyReachesPricingSoPlentyOfDriversMeansNoSurge(t *testing.T) {
	s, clk, _ := newSvc(t, Config{})
	for i := 0; i < 60; i++ {
		online(t, s, fmt.Sprintf("d%02d", i), pt(12.9700+float64(i)*0.0002, 77.5900))
	}
	s.Tick() // pricing now knows there are 60 drivers here
	for i := 0; i < 40; i++ {
		s.surge.RecordRequest(s.cells.Label(center), clk.Now())
	}
	clk.Advance(3 * time.Minute)
	online(t, s, "keepalive", center)
	for i := 0; i < 60; i++ {
		online(t, s, fmt.Sprintf("d%02d", i), pt(12.9700+float64(i)*0.0002, 77.5900))
	}
	s.Tick()
	if q, _ := s.Quote("alice", center, dropoff); q.Multiplier != 1 {
		t.Errorf("40 requests against 60 free drivers should not surge, got %.1f", q.Multiplier)
	}
}

func TestTheOfferHookIsCalledOncePerOfferWithTheDriverAndTrip(t *testing.T) {
	var calls []string
	clk := clock.NewFake(start)
	s, err := New(Config{Clock: clk, OnOffer: func(d, tr string) { calls = append(calls, d+">"+tr) }})
	if err != nil {
		t.Fatal(err)
	}
	online(t, s, "d1", pt(12.9710, 77.5900))
	online(t, s, "d2", pt(12.9750, 77.5900))
	tr := request(t, s, "alice", center)
	clk.Advance(11 * time.Second)
	s.Tick() // d1's offer lapses and d2 is offered the trip
	want := []string{"d1>" + tr.ID, "d2>" + tr.ID}
	if fmt.Sprint(calls) != fmt.Sprint(want) {
		t.Errorf("%v, want %v", calls, want)
	}
}
