package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ridesharing "github.com/Sumit6258/Faultline/systems/ride-sharing"
)

func post(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", path, strings.NewReader(body)))
	return rec
}

func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	b, _ := io.ReadAll(rec.Body)
	return string(b)
}

func TestMetricsDescribeWhatTheDispatcherDid(t *testing.T) {
	m := newMetrics()
	svc, err := ridesharing.New(ridesharing.Config{Observer: m})
	if err != nil {
		t.Fatal(err)
	}
	h := observe(ridesharing.NewHandler(svc), svc, m)

	before := scrape(t, h)
	for _, want := range []string{
		`rideshare_trips_requested_total 0`,
		`rideshare_trips_ended_total{outcome="no_drivers"} 0`, // exists before anything has happened, so it can be graphed
		`rideshare_offers_total{outcome="expired"} 0`,
		`rideshare_strategy_info{strategy="greedy",index="grid"} 1`,
	} {
		if !strings.Contains(before, want) {
			t.Errorf("before any traffic, missing %q", want)
		}
	}

	if rec := post(t, h, "/drivers/d1/location", `{"lat":12.971,"lon":77.59}`); rec.Code != 204 {
		t.Fatalf("%d", rec.Code)
	}
	rec := post(t, h, "/trips", `{"rider":"alice","from":{"lat":12.97,"lon":77.59},"to":{"lat":12.99,"lon":77.61}}`)
	if rec.Code != 201 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	post(t, h, "/drivers/d1/trips/trip-000001/accept", "")

	out := scrape(t, h)
	for _, want := range []string{
		`rideshare_trips_requested_total 1`,
		`rideshare_offers_total{outcome="offered"} 1`,
		`rideshare_offers_total{outcome="accepted"} 1`,
		`rideshare_match_wait_seconds_count 1`,
		`rideshare_drivers{state="busy"} 1`,
		`rideshare_drivers{state="available"} 0`,
		`rideshare_active_trips 1`,
		`rideshare_pending_trips 0`,
		`rideshare_geo_queries_total 1`,
		`http_requests_total{`,
		`route="POST /trips"`,
		`route="POST /drivers/{id}/trips/{trip}/accept"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("after a trip, missing %q\n%s", want, out)
		}
	}
}

func TestEveryCounterIsNamedLikeOne(t *testing.T) {
	m := newMetrics()
	svc, _ := ridesharing.New(ridesharing.Config{Observer: m})
	out := scrape(t, observe(ridesharing.NewHandler(svc), svc, m))
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "# TYPE rideshare_") && strings.HasSuffix(line, " counter") && !strings.Contains(line, "_total ") {
			t.Errorf("a counter that does not end in _total: %s", line)
		}
	}
}
