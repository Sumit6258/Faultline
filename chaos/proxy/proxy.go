// Package proxy is a reverse proxy that misbehaves on request. Put it in
// front of a service and the service's clients see the network failures that
// are hard to get any other way: a request that arrives but whose answer is
// lost, a request that never arrives, a dependency that goes slow or starts
// answering 503.
//
// The fault is set at runtime through a small control API, so an experiment
// can switch it on, watch what happens, and switch it off, without
// restarting anything. It needs no root access and no packet tooling, so it
// runs the same on a laptop as in CI.
package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sumit6258/Faultline/chaos/proxy/internal/obs"
)

type Mode string

const (
	// Latency delays each affected request before forwarding it.
	Latency Mode = "latency"
	// Error answers with an error status. The upstream never sees the request.
	Error Mode = "error"
	// DropRequest closes the connection without forwarding anything. The
	// request is lost on the way to the service.
	DropRequest Mode = "drop-request"
	// DropResponse forwards the request, lets the service finish it, then closes
	// the connection without sending the answer. The service did the work and
	// the client cannot know. This is the case idempotency keys exist for.
	DropResponse Mode = "drop-response"
	// Blackhole accepts the request and never answers, until the client gives up.
	Blackhole Mode = "blackhole"
)

// Fault describes what to do to matching requests.
type Fault struct {
	Mode Mode `json:"mode"`
	// Probability is the share of matching requests affected. Omitted means all.
	Probability *float64 `json:"probability,omitempty"`
	LatencyMS   int      `json:"latency_ms,omitempty"` // Latency mode
	JitterMS    int      `json:"jitter_ms,omitempty"`  // Latency mode, added at random up to this much
	Status      int      `json:"status,omitempty"`     // Error mode, default 503
	HoldMS      int      `json:"hold_ms,omitempty"`    // Blackhole mode, default 30000
	PathPrefix  string   `json:"path_prefix,omitempty"`
	Methods     []string `json:"methods,omitempty"`
}

// Validate rejects a fault that cannot work, so a typo is an error instead of
// a silently harmless experiment.
func (f *Fault) Validate() error {
	switch f.Mode {
	case Latency, Error, DropRequest, DropResponse, Blackhole:
	default:
		return fmt.Errorf("unknown mode %q, want one of latency, error, drop-request, drop-response, blackhole", f.Mode)
	}
	if f.Probability != nil && (*f.Probability < 0 || *f.Probability > 1) {
		return fmt.Errorf("probability must be between 0 and 1, got %v", *f.Probability)
	}
	if f.LatencyMS < 0 || f.LatencyMS > 60000 || f.JitterMS < 0 || f.JitterMS > 60000 {
		return errors.New("latency_ms and jitter_ms must be between 0 and 60000")
	}
	if f.Status != 0 && (f.Status < 400 || f.Status > 599) {
		return fmt.Errorf("status must be a 4xx or 5xx code, got %d", f.Status)
	}
	if f.HoldMS < 0 {
		return errors.New("hold_ms cannot be negative")
	}
	return nil
}

func (f *Fault) matches(r *http.Request) bool {
	if f.PathPrefix != "" && !strings.HasPrefix(r.URL.Path, f.PathPrefix) {
		return false
	}
	if len(f.Methods) > 0 {
		ok := false
		for _, m := range f.Methods {
			if strings.EqualFold(m, r.Method) {
				ok = true
			}
		}
		return ok
	}
	return true
}

type Proxy struct {
	rp       *httputil.ReverseProxy
	fault    atomic.Pointer[Fault]
	rngMu    sync.Mutex
	rng      *rand.Rand
	requests *obs.CounterVec
	duration *obs.HistogramVec
}

// Option adjusts a Proxy.
type Option func(*Proxy)

// WithSeed makes the choice of which requests are affected repeatable.
func WithSeed(seed int64) Option { return func(p *Proxy) { p.rng = rand.New(rand.NewSource(seed)) } }

// New returns a proxy to upstream. Its metrics are published through reg, and
// they are the view from the client's side of the proxy: what a user would
// have measured, including every delay the proxy added.
func New(upstream *url.URL, reg *obs.Registry, opts ...Option) *Proxy {
	p := &Proxy{rng: rand.New(rand.NewSource(time.Now().UnixNano()))}
	for _, o := range opts {
		o(p)
	}
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) { r.SetURL(upstream) },
		Transport: &http.Transport{
			MaxIdleConnsPerHost:   64,
			ResponseHeaderTimeout: 60 * time.Second,
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	p.requests = reg.NewCounterVec("chaosproxy_requests_total", "Requests through the proxy, by the fault applied and what happened to them.", "fault", "outcome")
	p.duration = reg.NewHistogramVec("chaosproxy_request_duration_seconds", "Time a client waited at the proxy, including injected delay.", obs.DefBuckets, "fault")
	return p
}

// Set replaces the active fault. Clear removes it.
func (p *Proxy) Set(f Fault) error {
	if err := f.Validate(); err != nil {
		return err
	}
	p.fault.Store(&f)
	return nil
}

func (p *Proxy) Clear() { p.fault.Store(nil) }

func (p *Proxy) Fault() *Fault { return p.fault.Load() }

func (p *Proxy) roll(prob *float64) bool {
	if prob == nil || *prob >= 1 {
		return true
	}
	if *prob <= 0 {
		return false
	}
	p.rngMu.Lock()
	defer p.rngMu.Unlock()
	return p.rng.Float64() < *prob
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	fault, outcome := "none", "forwarded"
	defer func() {
		p.requests.WithLabelValues(fault, outcome).Inc()
		p.duration.WithLabelValues(fault).Observe(time.Since(start).Seconds())
	}()

	f := p.fault.Load()
	if f == nil || !f.matches(r) || !p.roll(f.Probability) {
		p.rp.ServeHTTP(w, r)
		return
	}
	fault = string(f.Mode)

	switch f.Mode {
	case Latency:
		delay := time.Duration(f.LatencyMS) * time.Millisecond
		if f.JitterMS > 0 {
			p.rngMu.Lock()
			delay += time.Duration(p.rng.Intn(f.JitterMS+1)) * time.Millisecond
			p.rngMu.Unlock()
		}
		select {
		case <-time.After(delay):
			p.rp.ServeHTTP(w, r)
		case <-r.Context().Done():
			outcome = "client-gave-up"
		}
	case Error:
		status := f.Status
		if status == 0 {
			status = http.StatusServiceUnavailable
		}
		w.WriteHeader(status)
		outcome = "injected-error"
	case DropRequest:
		abort(w)
		outcome = "dropped-request"
	case DropResponse:
		// Run the whole exchange against the upstream, then throw the answer away.
		p.rp.ServeHTTP(&discard{header: http.Header{}}, r)
		abort(w)
		outcome = "dropped-response"
	case Blackhole:
		hold := time.Duration(f.HoldMS) * time.Millisecond
		if f.HoldMS == 0 {
			hold = 30 * time.Second
		}
		select {
		case <-r.Context().Done():
		case <-time.After(hold):
		}
		abort(w)
		outcome = "blackholed"
	}
}

// abort closes the client's connection without writing a response.
func abort(w http.ResponseWriter) {
	if hj, ok := w.(http.Hijacker); ok {
		if conn, _, err := hj.Hijack(); err == nil {
			_ = conn.Close()
			return
		}
	}
	w.WriteHeader(http.StatusBadGateway) // a writer that cannot be hijacked, only seen in tests
}

// discard is a ResponseWriter that swallows what the upstream says.
type discard struct{ header http.Header }

func (d *discard) Header() http.Header         { return d.header }
func (d *discard) Write(b []byte) (int, error) { return len(b), nil }
func (d *discard) WriteHeader(int)             {}
func (d *discard) Flush()                      {}

var _ io.Writer = (*discard)(nil)

// ControlHandler returns the API that sets and clears the fault:
//
//	GET    /fault   the active fault, or null
//	PUT    /fault   set one, from a JSON body
//	DELETE /fault   clear it
func (p *Proxy) ControlHandler() http.Handler {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /fault", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, map[string]any{"fault": p.Fault()})
	})
	mux.HandleFunc("PUT /fault", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var f Fault
		if err := dec.Decode(&f); err != nil {
			reply(w, http.StatusBadRequest, map[string]string{"error": "invalid fault: " + err.Error()})
			return
		}
		if err := p.Set(f); err != nil {
			reply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		reply(w, http.StatusOK, map[string]any{"fault": p.Fault()})
	})
	mux.HandleFunc("DELETE /fault", func(w http.ResponseWriter, r *http.Request) {
		p.Clear()
		reply(w, http.StatusOK, map[string]any{"fault": nil})
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, map[string]bool{"ok": true})
	})
	return mux
}
