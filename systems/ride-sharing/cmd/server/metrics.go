package main

import (
	"net/http"
	"time"

	ridesharing "github.com/Sumit6258/Faultline/systems/ride-sharing"
	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/obs"
	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/trip"
)

// metrics records what the dispatcher does. It is the service's Observer, so the service reports events to it,
// and observe wires up the numbers that are read from the service when Prometheus scrapes.
//
// The two numbers this system is judged by:
//
//   - rideshare_match_wait_seconds: how long a rider waits between asking and a driver saying yes. Its
//     95th percentile is what a rider feels.
//   - the share of requests that end in no_drivers, from rideshare_trips_ended_total. A rider who gives up
//     or is given up on is the failure the whole service exists to prevent.
type metrics struct {
	reg       *obs.Registry
	requested *obs.Counter
	ended     *obs.CounterVec
	offers    *obs.CounterVec
	wait      *obs.Histogram
}

func newMetrics() *metrics {
	reg := obs.NewRegistry()
	m := &metrics{
		reg:       reg,
		requested: reg.NewCounter("rideshare_trips_requested_total", "Trips riders have asked for."),
		ended:     reg.NewCounterVec("rideshare_trips_ended_total", "Trips that have ended, by how they ended.", "outcome"),
		offers:    reg.NewCounterVec("rideshare_offers_total", "Offers to drivers, by what happened to them.", "outcome"),
		wait: reg.NewHistogram("rideshare_match_wait_seconds", "Seconds between a rider asking and a driver accepting.",
			[]float64{0.5, 1, 2, 5, 10, 20, 30, 60, 120}),
	}
	// A series that does not exist yet cannot be graphed or alerted on, so create them all at zero.
	for _, o := range []string{"completed", "cancelled", "no_drivers"} {
		m.ended.WithLabelValues(o)
	}
	for _, o := range []string{"offered", "accepted", "declined", "expired"} {
		m.offers.WithLabelValues(o)
	}
	return m
}

func (m *metrics) TripRequested()                 { m.requested.Inc() }
func (m *metrics) OfferResult(outcome string)     { m.offers.WithLabelValues(outcome).Inc() }
func (m *metrics) TripMatched(wait time.Duration) { m.wait.Observe(wait.Seconds()) }
func (m *metrics) TripEnded(state trip.State)     { m.ended.WithLabelValues(string(state)).Inc() }

// observe adds a /metrics endpoint and per request metrics around the service's handler.
func observe(app http.Handler, svc *ridesharing.Service, m *metrics) http.Handler {
	reg := m.reg
	obs.RegisterRuntime(reg, "ride-sharing")
	reg.NewGaugeFuncVec("rideshare_drivers", "Drivers by state.", []string{"state"}, func() []obs.Sample {
		st := svc.Stats()
		var out []obs.Sample
		for _, s := range []ridesharing.DriverState{ridesharing.DriverAvailable, ridesharing.DriverReserved, ridesharing.DriverBusy, ridesharing.DriverOffline} {
			out = append(out, obs.Sample{Labels: []string{string(s)}, Value: float64(st.Drivers[s])})
		}
		return out
	})
	reg.NewGaugeFunc("rideshare_pending_trips", "Trips waiting for a driver.", func() float64 { return float64(svc.Stats().Pending) })
	reg.NewGaugeFunc("rideshare_active_trips", "Trips with a driver on them that are not over.", func() float64 { return float64(svc.Stats().Active) })
	reg.NewGaugeFunc("rideshare_surge_multiplier_max", "The highest surge multiplier in any neighbourhood.", func() float64 { return svc.Stats().SurgeMax })
	reg.NewCounterFunc("rideshare_geo_queries_total", "Nearest driver searches.", func() float64 { return float64(svc.Stats().Geo.Queries) })
	reg.NewCounterFunc("rideshare_geo_candidates_scanned_total", "Drivers examined by those searches. Divided by the queries, it is what the index costs.",
		func() float64 { return float64(svc.Stats().Geo.Scanned) })
	reg.NewCounterFunc("rideshare_dispatch_runs_total", "Times the matching strategy was run.", func() float64 { return float64(svc.Stats().Dispatches) })
	reg.NewGaugeFuncVec("rideshare_strategy_info", "Always 1. The labels say how matching and the driver index are configured.", []string{"strategy", "index"},
		func() []obs.Sample {
			st := svc.Stats()
			return []obs.Sample{{Labels: []string{string(st.Strategy), string(st.Index)}, Value: 1}}
		})
	return obs.NewHTTPMetrics(reg).Wrap(obs.Mount(app, "/metrics", reg.Handler()))
}
