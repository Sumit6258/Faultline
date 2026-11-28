package ridesharing

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/clock"
)

type api struct {
	t   *testing.T
	h   http.Handler
	clk *clock.Fake
}

func newAPI(t *testing.T) *api {
	s, clk, _ := newSvc(t, Config{})
	return &api{t, NewHandler(s), clk}
}

func (a *api) call(method, path, body string, headers ...string) (int, map[string]any, http.Header) {
	a.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	var out map[string]any
	// The service's own responses are JSON. The router's replies for an unknown path or a wrong method
	// (404 and 405) are the standard library's plain text, and are left as they are.
	if rec.Body.Len() > 0 && strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			a.t.Fatalf("%s %s: response is not JSON: %q", method, path, rec.Body.String())
		}
	}
	return rec.Code, out, rec.Header()
}

const tripBody = `{"rider":"alice","from":{"lat":12.97,"lon":77.59},"to":{"lat":12.99,"lon":77.61}}`

func (a *api) driver(id string, lat, lon float64) {
	a.t.Helper()
	b, _ := json.Marshal(map[string]float64{"lat": lat, "lon": lon})
	if code, _, _ := a.call("POST", "/drivers/"+id+"/location", string(b)); code != 204 {
		a.t.Fatalf("location: %d", code)
	}
}

func TestARideOverHTTP(t *testing.T) {
	a := newAPI(t)
	a.driver("d1", 12.971, 77.59)
	code, tr, hdr := a.call("POST", "/trips", tripBody)
	if code != 201 || tr["state"] != "offered" || tr["driver"] != "d1" || hdr.Get("Location") != "/trips/"+tr["id"].(string) {
		t.Fatalf("%d %v %v", code, tr, hdr)
	}
	id := tr["id"].(string)
	if tr["fare_cents"].(float64) <= 0 || tr["surge_multiplier"].(float64) != 1 {
		t.Errorf("%v", tr)
	}

	code, offer, _ := a.call("GET", "/drivers/d1/offer", "")
	if code != 200 || offer["trip_id"] != id || offer["pickup_distance_meters"].(float64) < 100 {
		t.Fatalf("offer: %d %v", code, offer)
	}
	if code, _, _ := a.call("GET", "/drivers/d2/offer", ""); code != 204 {
		t.Errorf("a driver with no offer gets 204, got %d", code)
	}

	for _, step := range []struct{ path, want string }{
		{"/drivers/d1/trips/" + id + "/accept", "accepted"},
		{"/drivers/d1/trips/" + id + "/arrive", "arrived"},
		{"/drivers/d1/trips/" + id + "/start", "in_progress"},
		{"/drivers/d1/trips/" + id + "/complete", "completed"},
	} {
		if code, got, _ := a.call("POST", step.path, ""); code != 200 || got["state"] != step.want {
			t.Fatalf("%s: %d %v", step.path, code, got)
		}
	}
	code, got, _ := a.call("GET", "/trips/"+id, "")
	hist, _ := got["history"].([]any)
	if code != 200 || got["state"] != "completed" || len(hist) != 5 {
		t.Errorf("%d %v", code, got)
	}
	if code, _, _ := a.call("GET", "/drivers/d1/offer", ""); code != 204 {
		t.Errorf("%d", code)
	}
}

func TestTheRiderSeesTheDriverMoving(t *testing.T) {
	a := newAPI(t)
	a.driver("d1", 12.971, 77.59)
	_, tr, _ := a.call("POST", "/trips", tripBody)
	a.driver("d1", 12.9705, 77.59)
	_, got, _ := a.call("GET", "/trips/"+tr["id"].(string), "")
	loc, _ := got["driver_location"].(map[string]any)
	if loc == nil || loc["lat"].(float64) != 12.9705 {
		t.Errorf("%v", got)
	}
}

func TestRetryingARequestWithTheSameKeyIsSafe(t *testing.T) {
	a := newAPI(t)
	code1, first, _ := a.call("POST", "/trips", tripBody, "Idempotency-Key", "abc")
	code2, again, hdr := a.call("POST", "/trips", tripBody, "Idempotency-Key", "abc")
	if code1 != 201 || code2 != 200 || first["id"] != again["id"] || hdr.Get("Location") != "" {
		t.Errorf("first %d, retry %d, ids %v %v", code1, code2, first["id"], again["id"])
	}
	other := strings.Replace(tripBody, "12.99", "13.01", 1)
	if code, body, _ := a.call("POST", "/trips", other, "Idempotency-Key", "abc"); code != 422 || body["code"] != "idempotency_conflict" {
		t.Errorf("the same key for a different trip: %d %v", code, body)
	}
}

func TestQuotesOverHTTP(t *testing.T) {
	a := newAPI(t)
	qbody := `{"rider":"alice","from":{"lat":12.97,"lon":77.59},"to":{"lat":12.99,"lon":77.61}}`
	code, q, _ := a.call("POST", "/quotes", qbody)
	if code != 200 || q["fare_cents"].(float64) <= 0 || q["distance_meters"].(float64) < 2000 {
		t.Fatalf("%d %v", code, q)
	}
	withQuote := strings.TrimSuffix(tripBody, "}") + `,"quote_id":"` + q["id"].(string) + `"}`
	code, tr, _ := a.call("POST", "/trips", withQuote)
	if code != 201 || tr["fare_cents"] != q["fare_cents"] {
		t.Fatalf("%d %v", code, tr)
	}
	a.call("POST", "/trips/"+tr["id"].(string)+"/cancel", "")
	if code, body, _ := a.call("POST", "/trips", withQuote); code != 409 || body["code"] != "quote_unusable" {
		t.Errorf("a used quote: %d %v", code, body)
	}
	_, q2, _ := a.call("POST", "/quotes", qbody)
	a.clk.Advance(3 * time.Minute)
	expired := strings.TrimSuffix(tripBody, "}") + `,"quote_id":"` + q2["id"].(string) + `"}`
	if code, body, _ := a.call("POST", "/trips", expired); code != 410 || body["code"] != "quote_expired" {
		t.Errorf("an expired quote: %d %v", code, body)
	}
	if code, body, _ := a.call("POST", "/trips", strings.TrimSuffix(tripBody, "}")+`,"quote_id":"quote-000000"}`); code != 404 {
		t.Errorf("an unknown quote: %d %v", code, body)
	}
}

func TestErrorsMapToTheStatusesAClientCanActOn(t *testing.T) {
	a := newAPI(t)
	a.driver("d1", 12.971, 77.59)
	_, tr, _ := a.call("POST", "/trips", tripBody)
	id := tr["id"].(string)
	a.driver("d2", 12.99, 77.59)

	for _, c := range []struct {
		name, method, path, body string
		status                   int
		code                     string
	}{
		{"not json", "POST", "/trips", "{nope", 400, "invalid_request"},
		{"unknown field", "POST", "/trips", `{"rider":"x","extra":1}`, 400, "invalid_request"},
		{"two documents", "POST", "/quotes", `{"rider":"x"} {"rider":"y"}`, 400, "invalid_request"},
		{"missing rider", "POST", "/trips", `{"from":{"lat":12.97,"lon":77.59},"to":{"lat":12.99,"lon":77.61}}`, 400, "invalid_request"},
		{"outside the area", "POST", "/trips", `{"rider":"bob","from":{"lat":40,"lon":77.59},"to":{"lat":12.99,"lon":77.61}}`, 400, "invalid_request"},
		{"driver outside the area", "POST", "/drivers/d9/location", `{"lat":0,"lon":0}`, 400, "invalid_request"},
		{"unknown trip", "GET", "/trips/trip-999999", "", 404, "not_found"},
		{"unknown driver", "POST", "/drivers/nobody/trips/" + id + "/accept", "", 404, "not_found"},
		{"wrong driver", "POST", "/drivers/d2/trips/" + id + "/accept", "", 409, "wrong_state"},
		{"out of order", "POST", "/drivers/d1/trips/" + id + "/start", "", 409, "wrong_state"},
		{"rider already has a trip", "POST", "/trips", tripBody, 409, "rider_busy"},
	} {
		code, body, _ := a.call(c.method, c.path, c.body)
		if code != c.status || body["code"] != c.code || body["error"] == "" {
			t.Errorf("%s: %d %v, want %d %s", c.name, code, body, c.status, c.code)
		}
	}
	a.call("POST", "/drivers/d1/trips/"+id+"/accept", "")
	if code, body, _ := a.call("POST", "/drivers/d1/offline", ""); code != 409 || body["code"] != "driver_busy" {
		t.Errorf("a driver on a trip going offline: %d %v", code, body)
	}
	a.call("POST", "/drivers/d1/trips/"+id+"/arrive", "")
	a.call("POST", "/drivers/d1/trips/"+id+"/start", "")
	if code, body, _ := a.call("POST", "/trips/"+id+"/cancel", ""); code != 409 {
		t.Errorf("cancelling a ride in progress: %d %v", code, body)
	}
}

func TestRoutesAndMethods(t *testing.T) {
	a := newAPI(t)
	for _, c := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/trips", 405},
		{"DELETE", "/trips/trip-000001", 405},
		{"GET", "/nowhere", 404},
		{"GET", "/stats", 200},
		{"POST", "/stats", 405},
	} {
		if code, _, _ := a.call(c.method, c.path, ""); code != c.status {
			t.Errorf("%s %s: %d, want %d", c.method, c.path, code, c.status)
		}
	}
}

func TestOversizedBodiesAreRefused(t *testing.T) {
	a := newAPI(t)
	big := `{"rider":"` + strings.Repeat("x", 2<<20) + `"}`
	if code, body, _ := a.call("POST", "/trips", big); code != 400 {
		t.Errorf("%d %v", code, body)
	}
}

func TestStatsSaysWhatTheServiceIsDoing(t *testing.T) {
	a := newAPI(t)
	a.driver("d1", 12.971, 77.59)
	a.call("POST", "/trips", tripBody)
	code, st, _ := a.call("GET", "/stats", "")
	drivers, _ := st["drivers"].(map[string]any)
	if code != 200 || st["strategy"] != "greedy" || st["index"] != "grid" || drivers["reserved"].(float64) != 1 || st["geo_queries"].(float64) < 1 {
		t.Errorf("%d %v", code, st)
	}
}

func TestJSONBodiesAreNotPaddedWithTheirOwnErrors(t *testing.T) {
	a := newAPI(t)
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(map[string]any{"rider": "alice", "from": map[string]float64{"lat": 12.97, "lon": 77.59}, "to": map[string]float64{"lat": 12.99, "lon": 77.61}})
	if code, _, _ := a.call("POST", "/trips", buf.String()); code != 201 {
		t.Errorf("a body with a trailing newline is fine: %d", code)
	}
}
