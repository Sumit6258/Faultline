package ridesharing

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/geo"
	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/trip"
)

type pointJSON struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

func (p pointJSON) point() geo.Point { return geo.Point{Lat: p.Lat, Lon: p.Lon} }
func pointOf(p geo.Point) pointJSON  { return pointJSON{p.Lat, p.Lon} }

type eventJSON struct {
	Kind   string    `json:"kind"`
	At     time.Time `json:"at"`
	Driver string    `json:"driver,omitempty"`
	By     string    `json:"by,omitempty"`
}

type tripJSON struct {
	ID             string      `json:"id"`
	Rider          string      `json:"rider"`
	State          string      `json:"state"`
	Pickup         pointJSON   `json:"pickup"`
	Dropoff        pointJSON   `json:"dropoff"`
	Driver         string      `json:"driver,omitempty"`
	DriverLocation *pointJSON  `json:"driver_location,omitempty"`
	FareCents      int64       `json:"fare_cents"`
	Multiplier     float64     `json:"surge_multiplier"`
	QuoteID        string      `json:"quote_id,omitempty"`
	CancelFeeCents int64       `json:"cancel_fee_cents,omitempty"`
	OfferExpiresAt *time.Time  `json:"offer_expires_at,omitempty"`
	Version        int         `json:"version"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
	History        []eventJSON `json:"history,omitempty"`
}

func tripOf(t trip.Trip, loc *geo.Point, history bool) tripJSON {
	j := tripJSON{ID: t.ID, Rider: t.Rider, State: string(t.State), Pickup: pointOf(t.Pickup), Dropoff: pointOf(t.Dropoff), Driver: t.Driver,
		FareCents: t.FareCents, Multiplier: t.Multiplier, QuoteID: t.QuoteID, CancelFeeCents: t.CancelFeeCents, Version: t.Version,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}
	if loc != nil {
		p := pointOf(*loc)
		j.DriverLocation = &p
	}
	if t.State == trip.Offered {
		d := t.OfferDeadline
		j.OfferExpiresAt = &d
	}
	if history {
		for _, e := range t.Events {
			j.History = append(j.History, eventJSON{Kind: string(e.Kind), At: e.At, Driver: e.Driver, By: e.By})
		}
	}
	return j
}

type errorJSON struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError turns the service's errors into the statuses a client can act on. 409 means the trip or the driver
// is not in a state where that is allowed, so retrying will not help until something else changes. 410 means the
// quote is gone and a new one is needed. 422 means the same idempotency key was used for a different request.
func writeError(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "internal"
	switch {
	case errors.Is(err, ErrInvalidRequest):
		status, code = http.StatusBadRequest, "invalid_request"
	case errors.Is(err, ErrTripNotFound), errors.Is(err, ErrDriverNotFound), errors.Is(err, ErrQuoteNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, ErrQuoteExpired):
		status, code = http.StatusGone, "quote_expired"
	case errors.Is(err, ErrIdempotencyConflict):
		status, code = http.StatusUnprocessableEntity, "idempotency_conflict"
	case errors.Is(err, ErrOfferExpired):
		status, code = http.StatusConflict, "offer_expired"
	case errors.Is(err, ErrRiderBusy):
		status, code = http.StatusConflict, "rider_busy"
	case errors.Is(err, ErrDriverBusy):
		status, code = http.StatusConflict, "driver_busy"
	case errors.Is(err, ErrQuoteUsed), errors.Is(err, ErrQuoteMismatch):
		status, code = http.StatusConflict, "quote_unusable"
	case errors.Is(err, ErrConflict):
		status, code = http.StatusConflict, "wrong_state"
	}
	writeJSON(w, status, errorJSON{Error: err.Error(), Code: code})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, fmt.Errorf("%w: body is not valid: %v", ErrInvalidRequest, err))
		return false
	}
	if _, err := dec.Token(); err != io.EOF {
		writeError(w, fmt.Errorf("%w: body has more than one JSON value", ErrInvalidRequest))
		return false
	}
	return true
}

// NewHandler exposes a Service over HTTP. It returns the *http.ServeMux itself, so the metrics middleware can
// label requests with the route that matched.
//
//	POST /quotes                              {"rider","from":{"lat","lon"},"to":{...}}
//	POST /trips                               {"rider","from","to","quote_id"?}, header Idempotency-Key
//	GET  /trips/{id}
//	POST /trips/{id}/cancel
//	POST /drivers/{id}/location               {"lat","lon"}
//	POST /drivers/{id}/offline
//	GET  /drivers/{id}/offer
//	POST /drivers/{id}/trips/{trip}/{accept|decline|arrive|start|complete|cancel}
//	GET  /stats
func NewHandler(svc *Service) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /quotes", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Rider string    `json:"rider"`
			From  pointJSON `json:"from"`
			To    pointJSON `json:"to"`
		}
		if !decode(w, r, &b) {
			return
		}
		q, err := svc.Quote(b.Rider, b.From.point(), b.To.point())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id": q.ID, "fare_cents": q.FareCents, "surge_multiplier": q.Multiplier, "distance_meters": int(q.Meters), "expires_at": q.ExpiresAt,
		})
	})

	mux.HandleFunc("POST /trips", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Rider   string    `json:"rider"`
			From    pointJSON `json:"from"`
			To      pointJSON `json:"to"`
			QuoteID string    `json:"quote_id"`
		}
		if !decode(w, r, &b) {
			return
		}
		tr, created, err := svc.RequestTrip(TripRequest{Rider: b.Rider, From: b.From.point(), To: b.To.point(), QuoteID: b.QuoteID, IdempotencyKey: r.Header.Get("Idempotency-Key")})
		if err != nil {
			writeError(w, err)
			return
		}
		status := http.StatusOK // a replay of an earlier request
		if created {
			status = http.StatusCreated
			w.Header().Set("Location", "/trips/"+tr.ID)
		}
		writeJSON(w, status, tripOf(tr, nil, false))
	})

	mux.HandleFunc("GET /trips/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := svc.GetTrip(r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tripOf(v.Trip, v.DriverLocation, true))
	})

	mux.HandleFunc("POST /trips/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		tr, err := svc.CancelTrip(r.PathValue("id"), "rider")
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tripOf(tr, nil, false))
	})

	mux.HandleFunc("POST /drivers/{id}/location", func(w http.ResponseWriter, r *http.Request) {
		var p pointJSON
		if !decode(w, r, &p) {
			return
		}
		if err := svc.UpdateLocation(r.PathValue("id"), p.point()); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /drivers/{id}/offline", func(w http.ResponseWriter, r *http.Request) {
		if err := svc.GoOffline(r.PathValue("id")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /drivers/{id}/offer", func(w http.ResponseWriter, r *http.Request) {
		o, ok := svc.CurrentOffer(r.PathValue("id"))
		if !ok {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"trip_id": o.TripID, "pickup": pointOf(o.Pickup), "dropoff": pointOf(o.Dropoff),
			"pickup_distance_meters": int(o.PickupMeters), "fare_cents": o.FareCents, "expires_at": o.ExpiresAt,
		})
	})

	driverAction := func(name string, do func(driver, tripID string) (trip.Trip, error)) {
		mux.HandleFunc("POST /drivers/{id}/trips/{trip}/"+name, func(w http.ResponseWriter, r *http.Request) {
			tr, err := do(r.PathValue("id"), r.PathValue("trip"))
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, tripOf(tr, nil, false))
		})
	}
	driverAction("accept", svc.AcceptOffer)
	driverAction("decline", svc.DeclineOffer)
	driverAction("arrive", svc.ArriveAtPickup)
	driverAction("start", svc.StartTrip)
	driverAction("complete", svc.CompleteTrip)
	driverAction("cancel", svc.DriverCancel)

	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		st := svc.Stats()
		scanned := 0.0
		if st.Geo.Queries > 0 {
			scanned = float64(st.Geo.Scanned) / float64(st.Geo.Queries)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"strategy": st.Strategy, "index": st.Index, "drivers": st.Drivers, "pending_trips": st.Pending, "active_trips": st.Active,
			"trips_stored": st.TripsStored, "surge_multiplier_max": st.SurgeMax, "dispatch_runs": st.Dispatches,
			"geo_queries": st.Geo.Queries, "geo_candidates_per_query": scanned,
		})
	})
	return mux
}
