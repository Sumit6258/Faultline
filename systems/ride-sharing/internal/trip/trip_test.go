package trip

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/geo"
)

var t0 = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

func newTrip() Trip {
	return New("t1", "rider1", geo.Point{Lat: 12.97, Lon: 77.59}, geo.Point{Lat: 12.99, Lon: 77.62}, "q1", 1200, 1.0, t0)
}

func ev(kind Kind, driver string) Event {
	return Event{Kind: kind, At: t0, Driver: driver, By: "", Deadline: t0.Add(10 * time.Second)}
}

func apply(t *testing.T, tr Trip, e Event) Trip {
	t.Helper()
	out, _, err := tr.Apply(e)
	if err != nil {
		t.Fatalf("%s while %s: %v", e.Kind, tr.State, err)
	}
	return out
}

func TestAHappyRide(t *testing.T) {
	tr := newTrip()
	steps := []struct {
		e    Event
		want State
	}{
		{ev(EvOffer, "d1"), Offered}, {ev(EvAccept, "d1"), Accepted}, {ev(EvArrive, "d1"), Arrived},
		{ev(EvStart, "d1"), InProgress}, {ev(EvComplete, "d1"), Completed},
	}
	for i, s := range steps {
		tr = apply(t, tr, s.e)
		if tr.State != s.want || tr.Version != i+1 || len(tr.Events) != i+1 {
			t.Fatalf("after %s: state %s version %d events %d", s.e.Kind, tr.State, tr.Version, len(tr.Events))
		}
	}
	if tr.Driver != "d1" {
		t.Errorf("the completed trip should still say who drove it: %q", tr.Driver)
	}
}

// Which events each state accepts. Everything else must be refused, and
// checking the whole grid is how a transition added by mistake gets noticed.
var allowed = map[State][]Kind{
	Requested:  {EvOffer, EvCancel, EvNoDrivers},
	Offered:    {EvAccept, EvDecline, EvOfferExpired, EvCancel},
	Accepted:   {EvArrive, EvCancel, EvDriverCancel},
	Arrived:    {EvStart, EvCancel, EvDriverCancel},
	InProgress: {EvComplete},
}

func inState(t *testing.T, s State) Trip {
	t.Helper()
	tr := newTrip()
	path := map[State][]Event{
		Requested:  nil,
		Offered:    {ev(EvOffer, "d1")},
		Accepted:   {ev(EvOffer, "d1"), ev(EvAccept, "d1")},
		Arrived:    {ev(EvOffer, "d1"), ev(EvAccept, "d1"), ev(EvArrive, "d1")},
		InProgress: {ev(EvOffer, "d1"), ev(EvAccept, "d1"), ev(EvArrive, "d1"), ev(EvStart, "d1")},
		Completed:  {ev(EvOffer, "d1"), ev(EvAccept, "d1"), ev(EvArrive, "d1"), ev(EvStart, "d1"), ev(EvComplete, "d1")},
		Cancelled:  {{Kind: EvCancel, At: t0, By: "rider"}},
		NoDrivers:  {{Kind: EvNoDrivers, At: t0, By: "system"}},
	}[s]
	for _, e := range path {
		tr = apply(t, tr, e)
	}
	if tr.State != s {
		t.Fatalf("setup: wanted %s, got %s", s, tr.State)
	}
	return tr
}

func TestEveryStateAndEventPairIsEitherAllowedOrRefused(t *testing.T) {
	for _, s := range []State{Requested, Offered, Accepted, Arrived, InProgress, Completed, Cancelled, NoDrivers} {
		ok := map[Kind]bool{}
		for _, k := range allowed[s] {
			ok[k] = true
		}
		for _, k := range AllKinds {
			tr := inState(t, s)
			e := Event{Kind: k, At: t0, Driver: "d1", By: "rider", Deadline: t0.Add(time.Second)}
			if k == EvOffer && s == Requested {
				e.Driver = "d2"
			}
			// An event that merely repeats the last one is a no-op, not an error, so use a different driver
			// for the cases where that could mask a refusal.
			out, changed, err := tr.Apply(e)
			switch {
			case ok[k] && err != nil:
				t.Errorf("%s while %s should be allowed: %v", k, s, err)
			case ok[k] && !changed:
				t.Errorf("%s while %s should change the trip", k, s)
			case !ok[k] && err == nil && changed:
				t.Errorf("%s while %s should be refused, but the trip became %s", k, s, out.State)
			case !ok[k] && err != nil && !errors.Is(err, ErrIllegalTransition):
				t.Errorf("%s while %s: the error should wrap ErrIllegalTransition: %v", k, s, err)
			}
		}
	}
}

func TestOnlyTheOfferedDriverCanAnswerAnOffer(t *testing.T) {
	tr := inState(t, Offered)
	for _, k := range []Kind{EvAccept, EvDecline, EvOfferExpired} {
		if _, _, err := tr.Apply(ev(k, "someone-else")); !errors.Is(err, ErrIllegalTransition) {
			t.Errorf("%s from another driver: %v", k, err)
		}
	}
	for _, s := range []State{Accepted, Arrived, InProgress} {
		tr := inState(t, s)
		k := map[State]Kind{Accepted: EvArrive, Arrived: EvStart, InProgress: EvComplete}[s]
		if _, _, err := tr.Apply(ev(k, "someone-else")); !errors.Is(err, ErrIllegalTransition) {
			t.Errorf("%s from a driver who is not on this trip: %v", k, err)
		}
	}
}

func TestDecliningSendsTheTripBackToWaitingAndTheDriverIsNotAskedAgain(t *testing.T) {
	tr := inState(t, Offered)
	tr = apply(t, tr, ev(EvDecline, "d1"))
	if tr.State != Requested || tr.Driver != "" || !tr.WasAttempted("d1") || !tr.OfferDeadline.IsZero() {
		t.Fatalf("%+v", tr)
	}
	if _, _, err := tr.Apply(ev(EvOffer, "d1")); !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("offering the trip again to a driver who turned it down: %v", err)
	}
	tr = apply(t, tr, ev(EvOffer, "d2"))
	if tr.Driver != "d2" || tr.State != Offered {
		t.Errorf("%+v", tr)
	}
}

func TestAnOfferLapsingAndADriverBackingOutBothReleaseTheDriver(t *testing.T) {
	lapsed := apply(t, inState(t, Offered), ev(EvOfferExpired, "d1"))
	backed := apply(t, inState(t, Accepted), ev(EvDriverCancel, "d1"))
	late := apply(t, inState(t, Arrived), ev(EvDriverCancel, "d1"))
	for name, tr := range map[string]Trip{"lapsed": lapsed, "backed out": backed, "backed out at the pickup": late} {
		if tr.State != Requested || tr.Driver != "" || !tr.WasAttempted("d1") {
			t.Errorf("%s: %+v", name, tr)
		}
	}
}

func TestACancellationFeeOnlyAppliesOnceADriverHasCommitted(t *testing.T) {
	cancel := Event{Kind: EvCancel, At: t0, By: "rider", FeeCents: 500}
	for s, wantFee := range map[State]int64{Requested: 0, Offered: 0, Accepted: 500, Arrived: 500} {
		tr := apply(t, inState(t, s), cancel)
		if tr.State != Cancelled || tr.CancelFeeCents != wantFee && s != Requested && s != Offered {
			t.Errorf("rider cancels while %s: state %s fee %d, want fee %d", s, tr.State, tr.CancelFeeCents, wantFee)
		}
		if (s == Requested || s == Offered) && tr.CancelFeeCents != 0 {
			t.Errorf("no fee before a driver commits, got %d while %s", tr.CancelFeeCents, s)
		}
	}
	bySystem := apply(t, inState(t, Accepted), Event{Kind: EvCancel, At: t0, By: "system", FeeCents: 500})
	if bySystem.CancelFeeCents != 0 {
		t.Errorf("the rider is not charged when the system cancels: %d", bySystem.CancelFeeCents)
	}
}

func TestARideInProgressCannotBeCancelled(t *testing.T) {
	if _, _, err := inState(t, InProgress).Apply(Event{Kind: EvCancel, At: t0, By: "rider"}); !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("%v", err)
	}
}

func TestRepeatingTheLastEventIsHarmless(t *testing.T) {
	for _, s := range []State{Offered, Accepted, Arrived, InProgress, Completed, Cancelled, NoDrivers} {
		tr := inState(t, s)
		last := tr.Events[len(tr.Events)-1]
		again, changed, err := tr.Apply(last)
		if err != nil || changed || again.Version != tr.Version || len(again.Events) != len(tr.Events) {
			t.Errorf("repeating %s while %s: changed=%v err=%v version %d to %d", last.Kind, s, changed, err, tr.Version, again.Version)
		}
	}
	// A retried decline after the trip moved on to another driver is also harmless.
	tr := apply(t, apply(t, inState(t, Offered), ev(EvDecline, "d1")), ev(EvOffer, "d2"))
	if _, changed, err := tr.Apply(ev(EvDecline, "d1")); !errors.Is(err, ErrIllegalTransition) || changed {
		t.Errorf("a late decline from the previous driver must not disturb the new offer: %v", err)
	}
}

func TestTerminalTripsRefuseEverythingElse(t *testing.T) {
	for _, s := range []State{Completed, Cancelled, NoDrivers} {
		tr := inState(t, s)
		for _, k := range AllKinds {
			e := Event{Kind: k, At: t0, Driver: "d9", By: "rider"}
			if out, changed, err := tr.Apply(e); err == nil || changed || out.State != s {
				t.Errorf("%s is over, but %s gave state %s changed=%v err=%v", s, k, out.State, changed, err)
			}
		}
	}
}

func TestAnOfferNeedsADriver(t *testing.T) {
	if _, _, err := newTrip().Apply(Event{Kind: EvOffer, At: t0}); !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("%v", err)
	}
}

func TestApplyDoesNotDisturbTheTripItWasCalledOn(t *testing.T) {
	tr := apply(t, newTrip(), ev(EvOffer, "d1"))
	before := fmt.Sprintf("%+v", tr)
	next := apply(t, tr, ev(EvDecline, "d1"))
	next = apply(t, next, ev(EvOffer, "d2"))
	if after := fmt.Sprintf("%+v", tr); after != before {
		t.Errorf("the original changed:\n%s\n%s", before, after)
	}
	_ = next
}

// Whatever order events arrive in, from whoever, some of them nonsense, the
// trip must never end up in a state the table does not describe.
func TestRandomEventsNeverBreakTheInvariants(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	drivers := []string{"d1", "d2", "d3", ""}
	hasDriver := map[State]bool{Offered: true, Accepted: true, Arrived: true, InProgress: true, Completed: true}
	valid := map[State]bool{Requested: true, Offered: true, Accepted: true, Arrived: true, InProgress: true, Completed: true, Cancelled: true, NoDrivers: true}
	for run := 0; run < 2000; run++ {
		tr := newTrip()
		changes := 0
		for step := 0; step < 40; step++ {
			e := Event{Kind: AllKinds[r.Intn(len(AllKinds))], At: t0.Add(time.Duration(step) * time.Second), Driver: drivers[r.Intn(len(drivers))],
				By: []string{"rider", "system", ""}[r.Intn(3)], FeeCents: 500}
			wasTerminal := tr.State.Terminal()
			stateBefore := tr.State
			next, changed, err := tr.Apply(e)
			if err != nil && changed {
				t.Fatal("an error and a change together")
			}
			if changed {
				changes++
				if wasTerminal {
					t.Fatalf("a finished trip changed: %s -> %s by %s", stateBefore, next.State, e.Kind)
				}
			}
			if !changed && (next.Version != tr.Version || len(next.Events) != len(tr.Events)) {
				t.Fatal("an unchanged trip gained history")
			}
			tr = next
			if !valid[tr.State] {
				t.Fatalf("invalid state %q", tr.State)
			}
			// A waiting trip has no driver, and a trip with a driver on it names one. A cancelled trip may
			// still name the driver who was on it, which is how the dispatcher knows whom to free.
			if tr.State != Cancelled && hasDriver[tr.State] != (tr.Driver != "") {
				t.Fatalf("state %s with driver %q", tr.State, tr.Driver)
			}
			if tr.Version != changes || len(tr.Events) != changes {
				t.Fatalf("version %d, events %d, changes %d", tr.Version, len(tr.Events), changes)
			}
			seen := map[string]bool{}
			for _, d := range tr.Attempted {
				if seen[d] || d == tr.Driver || d == "" {
					t.Fatalf("attempted list %v with current driver %q", tr.Attempted, tr.Driver)
				}
				seen[d] = true
			}
		}
	}
}

func TestACancelledTripNamesTheDriverWhoWasOnIt(t *testing.T) {
	cancel := Event{Kind: EvCancel, At: t0, By: "rider", FeeCents: 500}
	for s, want := range map[State]string{Requested: "", Offered: "d1", Accepted: "d1", Arrived: "d1"} {
		if got := apply(t, inState(t, s), cancel).Driver; got != want {
			t.Errorf("cancelled while %s: driver %q, want %q", s, got, want)
		}
	}
}
