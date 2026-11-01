package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sumit6258/Faultline/chaos/proxy/internal/obs"
)

type rig struct {
	t        *testing.T
	proxy    *Proxy
	reg      *obs.Registry
	front    *httptest.Server // what clients talk to
	calls    atomic.Int64     // requests that reached the upstream
	bodies   atomic.Int64     // total request body bytes that reached the upstream
	client   *http.Client
	upstream *httptest.Server
}

func newRig(t *testing.T, opts ...Option) *rig {
	t.Helper()
	r := &rig{t: t, reg: obs.NewRegistry()}
	r.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.calls.Add(1)
		n, _ := io.Copy(io.Discard, req.Body)
		r.bodies.Add(n)
		w.Header().Set("X-From-Upstream", "yes")
		w.Header().Set("X-Saw-Key", req.Header.Get("Idempotency-Key"))
		io.WriteString(w, "upstream saw "+req.Method+" "+req.URL.RequestURI())
	}))
	u, _ := url.Parse(r.upstream.URL)
	r.proxy = New(u, r.reg, opts...)
	r.front = httptest.NewServer(r.proxy)
	// A fresh connection every time, so the client never silently retries a request on a stale one.
	r.client = &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	t.Cleanup(func() {
		r.front.CloseClientConnections()
		r.front.Close()
		r.upstream.Close()
	})
	return r
}

func (r *rig) do(method, path string, body io.Reader) (*http.Response, error) {
	req, _ := http.NewRequest(method, r.front.URL+path, body)
	req.Header.Set("Idempotency-Key", "k-123")
	return r.client.Do(req)
}

func (r *rig) set(f Fault) {
	r.t.Helper()
	if err := r.proxy.Set(f); err != nil {
		r.t.Fatal(err)
	}
}

func fp(v float64) *float64 { return &v }

func (r *rig) scrape() string {
	var b strings.Builder
	r.reg.WriteTo(&b)
	return b.String()
}

func TestPassesRequestsThroughUntouchedWhenNoFaultIsSet(t *testing.T) {
	r := newRig(t)
	resp, err := r.do("GET", "/things?id=7", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(b) != "upstream saw GET /things?id=7" {
		t.Fatalf("got %d %q", resp.StatusCode, b)
	}
	if resp.Header.Get("X-From-Upstream") != "yes" || resp.Header.Get("X-Saw-Key") != "k-123" {
		t.Errorf("headers were not carried both ways: %v", resp.Header)
	}
	if r.calls.Load() != 1 {
		t.Errorf("upstream calls: %d", r.calls.Load())
	}
}

func TestLargeBodiesArriveIntact(t *testing.T) {
	r := newRig(t)
	body := bytes.Repeat([]byte("x"), 2<<20)
	resp, err := r.do("POST", "/upload", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if r.bodies.Load() != int64(len(body)) {
		t.Fatalf("upstream received %d bytes, sent %d", r.bodies.Load(), len(body))
	}
}

func TestLatencyDelaysTheRequestAndStillDeliversIt(t *testing.T) {
	r := newRig(t)
	r.set(Fault{Mode: Latency, LatencyMS: 150})
	start := time.Now()
	resp, err := r.do("GET", "/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if d := time.Since(start); d < 140*time.Millisecond {
		t.Errorf("took %v, want at least 150ms", d)
	}
	if resp.StatusCode != 200 || r.calls.Load() != 1 {
		t.Errorf("a delayed request must still succeed: %d, upstream calls %d", resp.StatusCode, r.calls.Load())
	}
}

func TestLatencyJitterStaysWithinItsBound(t *testing.T) {
	r := newRig(t, WithSeed(1))
	r.set(Fault{Mode: Latency, LatencyMS: 50, JitterMS: 100})
	var slowest, fastest time.Duration = 0, time.Hour
	for i := 0; i < 12; i++ {
		start := time.Now()
		resp, err := r.do("GET", "/x", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		d := time.Since(start)
		if d > slowest {
			slowest = d
		}
		if d < fastest {
			fastest = d
		}
	}
	if fastest < 45*time.Millisecond || slowest > 400*time.Millisecond {
		t.Errorf("latencies ranged %v to %v, want roughly 50ms to 150ms", fastest, slowest)
	}
	if slowest-fastest < 20*time.Millisecond {
		t.Errorf("jitter made no visible difference: %v to %v", fastest, slowest)
	}
}

func TestErrorAnswersWithoutTroublingTheUpstream(t *testing.T) {
	r := newRig(t)
	r.set(Fault{Mode: Error})
	resp, _ := r.do("GET", "/x", nil)
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Errorf("default status %d, want 503", resp.StatusCode)
	}
	r.set(Fault{Mode: Error, Status: 500})
	resp, _ = r.do("GET", "/x", nil)
	resp.Body.Close()
	if resp.StatusCode != 500 {
		t.Errorf("status %d, want 500", resp.StatusCode)
	}
	if r.calls.Load() != 0 {
		t.Errorf("the upstream saw %d requests, it should have seen none", r.calls.Load())
	}
}

func TestDropRequestLosesTheRequestBeforeTheService(t *testing.T) {
	r := newRig(t)
	r.set(Fault{Mode: DropRequest})
	resp, err := r.do("POST", "/payments", strings.NewReader(`{}`))
	if err == nil {
		resp.Body.Close()
		t.Fatal("the client should have seen the connection die")
	}
	if r.calls.Load() != 0 {
		t.Errorf("the upstream saw %d requests, it should have seen none", r.calls.Load())
	}
}

// The case idempotency keys exist for: the work was done, and the client
// cannot tell.
func TestDropResponseLetsTheServiceFinishAndThenLosesTheAnswer(t *testing.T) {
	r := newRig(t)
	r.set(Fault{Mode: DropResponse})
	resp, err := r.do("POST", "/payments", strings.NewReader(`{"amount_cents": 5}`))
	if err == nil {
		resp.Body.Close()
		t.Fatal("the client should not have received an answer")
	}
	if r.calls.Load() != 1 {
		t.Fatalf("the upstream handled %d requests, want exactly 1: the request must arrive and complete", r.calls.Load())
	}
	if r.bodies.Load() != int64(len(`{"amount_cents": 5}`)) {
		t.Errorf("the upstream got %d body bytes", r.bodies.Load())
	}
	if !strings.Contains(r.scrape(), `chaosproxy_requests_total{fault="drop-response",outcome="dropped-response"} 1`) {
		t.Errorf("the drop should be counted:\n%s", r.scrape())
	}
}

func TestBlackholeHoldsTheRequestUntilTheClientGivesUp(t *testing.T) {
	r := newRig(t)
	r.set(Fault{Mode: Blackhole})
	r.client.Timeout = 200 * time.Millisecond
	start := time.Now()
	_, err := r.do("GET", "/x", nil)
	if err == nil {
		t.Fatal("expected a timeout")
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Errorf("expected a timeout, got %v", err)
	}
	if time.Since(start) < 190*time.Millisecond {
		t.Error("it answered before the client's timeout")
	}
	if r.calls.Load() != 0 {
		t.Error("a blackholed request must not reach the upstream")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(r.scrape(), `outcome="blackholed"`) {
		if time.Now().After(deadline) {
			t.Fatal("the proxy never noticed the client had left, so it would hold the goroutine for the whole hold time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestProbabilityControlsTheShareOfRequestsAffected(t *testing.T) {
	cases := []struct {
		p        float64
		min, max int
	}{{0, 0, 0}, {1, 400, 400}, {0.5, 160, 240}, {0.1, 15, 70}}
	for _, c := range cases {
		r := newRig(t, WithSeed(7))
		r.set(Fault{Mode: Error, Probability: fp(c.p)})
		failed := 0
		for i := 0; i < 400; i++ {
			resp, err := r.do("GET", "/x", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode == 503 {
				failed++
			}
		}
		if failed < c.min || failed > c.max {
			t.Errorf("probability %v: %d of 400 failed, want %d to %d", c.p, failed, c.min, c.max)
		}
	}
}

func TestFaultsCanBeLimitedToAPathAndAMethod(t *testing.T) {
	r := newRig(t)
	r.set(Fault{Mode: Error, PathPrefix: "/payments", Methods: []string{"post"}})
	status := func(method, path string) int {
		resp, err := r.do(method, path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := status("POST", "/payments"); got != 503 {
		t.Errorf("POST /payments: %d, want 503", got)
	}
	if got := status("POST", "/payments/abc"); got != 503 {
		t.Errorf("POST /payments/abc: %d, want 503, a prefix match", got)
	}
	if got := status("GET", "/payments"); got != 200 {
		t.Errorf("GET /payments: %d, want 200, the method does not match", got)
	}
	if got := status("POST", "/health"); got != 200 {
		t.Errorf("POST /health: %d, want 200, the path does not match", got)
	}
}

func TestClearAndSetChangeTheBehaviourAtOnce(t *testing.T) {
	r := newRig(t)
	r.set(Fault{Mode: Error})
	if resp, _ := r.do("GET", "/x", nil); resp.StatusCode != 503 {
		t.Fatal("fault not applied")
	}
	r.proxy.Clear()
	if resp, _ := r.do("GET", "/x", nil); resp.StatusCode != 200 {
		t.Fatal("clearing the fault should restore normal service")
	}
	r.set(Fault{Mode: Error, Status: 502})
	r.set(Fault{Mode: Error, Status: 500})
	if resp, _ := r.do("GET", "/x", nil); resp.StatusCode != 500 {
		t.Fatal("the newer fault should replace the older one")
	}
}

func TestAnUpstreamThatIsDownIsA502NotAHang(t *testing.T) {
	r := newRig(t)
	r.upstream.Close()
	resp, err := r.do("GET", "/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status %d, want 502", resp.StatusCode)
	}
}

func TestInvalidFaultsAreRejected(t *testing.T) {
	bad := []Fault{
		{Mode: "explode"},
		{Mode: ""},
		{Mode: Error, Probability: fp(1.5)},
		{Mode: Error, Probability: fp(-0.1)},
		{Mode: Latency, LatencyMS: -1},
		{Mode: Latency, LatencyMS: 600000},
		{Mode: Error, Status: 200},
		{Mode: Error, Status: 700},
		{Mode: Blackhole, HoldMS: -5},
	}
	r := newRig(t)
	for _, f := range bad {
		if err := r.proxy.Set(f); err == nil {
			t.Errorf("%+v should have been rejected", f)
		}
	}
	if r.proxy.Fault() != nil {
		t.Error("a rejected fault must not become active")
	}
}

func TestMetricsShowWhatWasDoneAndWhatTheClientExperienced(t *testing.T) {
	r := newRig(t)
	r.do("GET", "/a", nil)
	r.set(Fault{Mode: Latency, LatencyMS: 100})
	r.do("GET", "/b", nil)
	r.set(Fault{Mode: Error})
	r.do("GET", "/c", nil)

	out := r.scrape()
	for _, want := range []string{
		`chaosproxy_requests_total{fault="none",outcome="forwarded"} 1`,
		`chaosproxy_requests_total{fault="latency",outcome="forwarded"} 1`,
		`chaosproxy_requests_total{fault="error",outcome="injected-error"} 1`,
		// the delayed request took over 50ms but under 250ms, as the client saw it
		`chaosproxy_request_duration_seconds_bucket{fault="latency",le="0.05"} 0`,
		`chaosproxy_request_duration_seconds_bucket{fault="latency",le="0.25"} 1`,
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestFaultsCanBeChangedWhileRequestsAreInFlight(t *testing.T) {
	r := newRig(t, WithSeed(3))
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if resp, err := r.do("GET", "/x", nil); err == nil {
						resp.Body.Close()
					}
				}
			}
		}()
	}
	modes := []Fault{{Mode: Error, Probability: fp(0.5)}, {Mode: DropRequest, Probability: fp(0.3)}, {Mode: Latency, LatencyMS: 2, JitterMS: 3}, {Mode: DropResponse, Probability: fp(0.5)}}
	for i := 0; i < 60; i++ {
		r.set(modes[i%len(modes)])
		if i%5 == 0 {
			r.proxy.Clear()
		}
		time.Sleep(3 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
}

func control(t *testing.T, h http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestControlAPISetsReadsAndClearsTheFault(t *testing.T) {
	r := newRig(t)
	h := r.proxy.ControlHandler()

	if code, out := control(t, h, "GET", "/fault", ""); code != 200 || out["fault"] != nil {
		t.Fatalf("no fault yet: %d %v", code, out)
	}
	code, out := control(t, h, "PUT", "/fault", `{"mode":"drop-response","path_prefix":"/payments","probability":0.25}`)
	if code != 200 {
		t.Fatalf("set: %d %v", code, out)
	}
	active := r.proxy.Fault()
	if active == nil || active.Mode != DropResponse || active.PathPrefix != "/payments" || *active.Probability != 0.25 {
		t.Fatalf("active fault %+v", active)
	}
	if _, out := control(t, h, "GET", "/fault", ""); out["fault"] == nil {
		t.Error("GET should report the active fault")
	}
	if code, _ := control(t, h, "DELETE", "/fault", ""); code != 200 || r.proxy.Fault() != nil {
		t.Error("DELETE should clear the fault")
	}
	if code, out := control(t, h, "GET", "/health", ""); code != 200 || out["ok"] != true {
		t.Errorf("health: %d %v", code, out)
	}
}

func TestControlAPIRejectsMistakesWithAMessage(t *testing.T) {
	r := newRig(t)
	h := r.proxy.ControlHandler()
	for name, body := range map[string]string{
		"not json":      `mode=error`,
		"unknown field": `{"mode":"error","probabilty":0.5}`,
		"unknown mode":  `{"mode":"explode"}`,
		"bad value":     `{"mode":"error","probability":2}`,
		"empty":         ``,
	} {
		code, out := control(t, h, "PUT", "/fault", body)
		if code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, code)
		}
		if msg, _ := out["error"].(string); msg == "" {
			t.Errorf("%s: no error message in %v", name, out)
		}
	}
	if r.proxy.Fault() != nil {
		t.Error("a rejected request must not change the fault")
	}
}
