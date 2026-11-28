// Package trip is the life of one ride as a state machine.
//
// A ride touches a rider's phone, a driver's phone and the dispatcher, each of
// which can retry, time out or fail on its own. Without an explicit table of
// what may follow what, those races turn into a rider charged for a ride that
// was cancelled, or a driver completing a trip someone else is on. Every
// change goes through Apply, which refuses anything the table does not allow
// and treats a repeated event as a no-op, so a retried request is harmless.
package trip

import (
	"errors"
	"fmt"
	"time"

	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/geo"
)

type State string

const (
	Requested  State = "requested"   // waiting for a driver
	Offered    State = "offered"     // a driver has been asked and has not answered
	Accepted   State = "accepted"    // a driver is on the way
	Arrived    State = "arrived"     // the driver is at the pickup
	InProgress State = "in_progress" // the rider is in the car
	Completed  State = "completed"
	Cancelled  State = "cancelled"
	NoDrivers  State = "no_drivers" // nobody took it in the time allowed
)

func (s State) Terminal() bool { return s == Completed || s == Cancelled || s == NoDrivers }

type Kind string

const (
	EvOffer        Kind = "offer"
	EvAccept       Kind = "accept"
	EvDecline      Kind = "decline"
	EvOfferExpired Kind = "offer_expired"
	EvArrive       Kind = "arrive"
	EvStart        Kind = "start"
	EvComplete     Kind = "complete"
	EvCancel       Kind = "cancel"        // by the rider, or by the system
	EvDriverCancel Kind = "driver_cancel" // the driver backs out after accepting, and the rider goes back to waiting
	EvNoDrivers    Kind = "no_drivers"
)

// AllKinds lists every kind of event, for tests that check the whole table.
var AllKinds = []Kind{EvOffer, EvAccept, EvDecline, EvOfferExpired, EvArrive, EvStart, EvComplete, EvCancel, EvDriverCancel, EvNoDrivers}

type Event struct {
	Kind     Kind
	At       time.Time
	Driver   string    // the driver the event is about, for everything except cancel by the rider and no_drivers
	By       string    // who cancelled: "rider" or "system"
	Deadline time.Time // for an offer, when it lapses
	FeeCents int64     // for a cancel after a driver has committed, what the rider is charged
	Reason   string
}

type Trip struct {
	ID, Rider       string
	Pickup, Dropoff geo.Point
	QuoteID         string
	FareCents       int64
	Multiplier      float64
	State           State
	Driver          string
	Attempted       []string // drivers who declined, let an offer lapse, or backed out, and are not asked again
	OfferDeadline   time.Time
	CancelFeeCents  int64
	Version         int
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Events          []Event
}

var ErrIllegalTransition = errors.New("trip: transition not allowed")

type IllegalError struct {
	State  State
	Kind   Kind
	Detail string
}

func (e *IllegalError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("trip: %s is not allowed while %s: %s", e.Kind, e.State, e.Detail)
	}
	return fmt.Sprintf("trip: %s is not allowed while %s", e.Kind, e.State)
}

func (e *IllegalError) Unwrap() error { return ErrIllegalTransition }

func New(id, rider string, pickup, dropoff geo.Point, quoteID string, fareCents int64, multiplier float64, now time.Time) Trip {
	return Trip{ID: id, Rider: rider, Pickup: pickup, Dropoff: dropoff, QuoteID: quoteID, FareCents: fareCents,
		Multiplier: multiplier, State: Requested, CreatedAt: now, UpdatedAt: now}
}

func (t Trip) WasAttempted(driver string) bool {
	for _, d := range t.Attempted {
		if d == driver {
			return true
		}
	}
	return false
}

// Apply returns the trip after ev, and whether anything changed. It never
// modifies t. An event that repeats the one that produced the current state
// changes nothing and succeeds, which is what lets a client retry a request whose
// response it never saw.
func (t Trip) Apply(ev Event) (Trip, bool, error) {
	if n := len(t.Events); n > 0 {
		if last := t.Events[n-1]; last.Kind == ev.Kind && last.Driver == ev.Driver && last.By == ev.By {
			return t, false, nil
		}
	}
	illegal := func(detail string) (Trip, bool, error) {
		return t, false, &IllegalError{State: t.State, Kind: ev.Kind, Detail: detail}
	}
	if t.State.Terminal() {
		return illegal("the trip is over")
	}
	forDriver := func() bool { return ev.Driver != "" && ev.Driver == t.Driver }

	out := t
	out.Attempted = append([]string(nil), t.Attempted...)
	out.Events = append(append([]Event(nil), t.Events...), ev)
	release := func() { // the driver is no longer on this trip, and is not asked again
		out.Attempted = append(out.Attempted, t.Driver)
		out.Driver = ""
		out.OfferDeadline = time.Time{}
		out.State = Requested
	}

	switch t.State {
	case Requested:
		switch ev.Kind {
		case EvOffer:
			if ev.Driver == "" {
				return illegal("an offer needs a driver")
			}
			if t.WasAttempted(ev.Driver) {
				return illegal("that driver has already turned this trip down")
			}
			out.State, out.Driver, out.OfferDeadline = Offered, ev.Driver, ev.Deadline
		case EvCancel:
			out.State = Cancelled
		case EvNoDrivers:
			out.State = NoDrivers
		default:
			return illegal("")
		}
	case Offered:
		switch {
		case ev.Kind == EvAccept && forDriver():
			out.State, out.OfferDeadline = Accepted, time.Time{}
		case (ev.Kind == EvDecline || ev.Kind == EvOfferExpired) && forDriver():
			release()
		case ev.Kind == EvCancel:
			out.State, out.OfferDeadline = Cancelled, time.Time{}
		case ev.Kind == EvAccept || ev.Kind == EvDecline || ev.Kind == EvOfferExpired:
			return illegal("not the driver this trip was offered to")
		default:
			return illegal("")
		}
	case Accepted, Arrived:
		switch {
		case ev.Kind == EvArrive && t.State == Accepted && forDriver():
			out.State = Arrived
		case ev.Kind == EvStart && t.State == Arrived && forDriver():
			out.State = InProgress
		case ev.Kind == EvCancel:
			out.State = Cancelled
			if ev.By == "rider" {
				out.CancelFeeCents = ev.FeeCents
			}
		case ev.Kind == EvDriverCancel && forDriver():
			release()
		case ev.Kind == EvArrive || ev.Kind == EvStart || ev.Kind == EvDriverCancel:
			if !forDriver() {
				return illegal("not the assigned driver")
			}
			return illegal("")
		default:
			return illegal("")
		}
	case InProgress:
		if ev.Kind == EvComplete && forDriver() {
			out.State = Completed
		} else if ev.Kind == EvComplete {
			return illegal("not the assigned driver")
		} else {
			return illegal("a ride in progress can only be completed")
		}
	}
	out.Version++
	out.UpdatedAt = ev.At
	return out, true, nil
}
