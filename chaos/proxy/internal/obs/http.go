package obs

import (
	"net/http"
	"strconv"
	"time"
)

// HTTPMetrics records the three numbers every request/response service should
// publish, often called RED: Rate, Errors and Duration.
//
//	http_requests_total{method,route,status}
//	http_request_duration_seconds{method,route}
//	http_requests_in_flight
//
// The route label is the pattern the mux matched, such as "POST /follow" or
// "/timeline/", never the URL path. A path carries user ids and short codes,
// and one series per id would be exactly the cardinality explosion MaxSeries
// exists to stop.
type HTTPMetrics struct {
	requests *CounterVec
	duration *HistogramVec
	inFlight *Gauge

	skipDuration map[string]bool
	skipPath     string
}

// HTTPOption adjusts the middleware.
type HTTPOption func(*HTTPMetrics)

// SkipDuration leaves the named routes out of the latency histogram while
// still counting them. Use it for long lived responses such as event
// streams, whose "duration" is how long the browser stayed connected, which
// would bury every real latency in the top bucket.
func SkipDuration(routes ...string) HTTPOption {
	return func(m *HTTPMetrics) {
		for _, r := range routes {
			m.skipDuration[r] = true
		}
	}
}

func NewHTTPMetrics(reg *Registry, opts ...HTTPOption) *HTTPMetrics {
	m := &HTTPMetrics{
		requests:     reg.NewCounterVec("http_requests_total", "HTTP requests handled, by method, matched route and status code.", "method", "route", "status"),
		duration:     reg.NewHistogramVec("http_request_duration_seconds", "HTTP request latency in seconds, by method and matched route.", DefBuckets, "method", "route"),
		inFlight:     reg.NewGauge("http_requests_in_flight", "HTTP requests currently being handled."),
		skipDuration: map[string]bool{},
		skipPath:     "/metrics",
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// patterner is satisfied by *http.ServeMux. It reports which registered
// pattern would handle a request, without running the handler.
type patterner interface {
	Handler(*http.Request) (http.Handler, string)
}

// Wrap instruments h. Pass the *http.ServeMux itself, or a handler that is
// one, so the matched pattern can be used as the route label. Requests to
// /metrics are served untouched, so scraping does not show up as traffic.
func (m *HTTPMetrics) Wrap(h http.Handler) http.Handler {
	mux, _ := h.(patterner)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == m.skipPath {
			h.ServeHTTP(w, r)
			return
		}
		route := "unmatched"
		if mux != nil {
			if _, p := mux.Handler(r); p != "" {
				route = p
			}
		}
		method := normalizeMethod(r.Method)

		m.inFlight.Inc()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		defer func() {
			// Recorded in a defer so a panicking handler still counts, as a 500.
			rec := recover()
			if rec != nil {
				sw.status = http.StatusInternalServerError
			}
			m.inFlight.Dec()
			if !m.skipDuration[route] {
				m.duration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
			}
			m.requests.WithLabelValues(method, route, strconv.Itoa(sw.status)).Inc()
			if rec != nil {
				panic(rec)
			}
		}()
		h.ServeHTTP(sw, r)
	})
}

// normalizeMethod keeps the method label to a fixed set. A client can send
// any string as a method, and each new one would otherwise be a new series.
func normalizeMethod(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return m
	}
	return "OTHER"
}

// statusWriter remembers the status code. It also forwards Flush, because
// streaming handlers type assert their writer to http.Flusher, and Unwrap, so
// http.ResponseController can reach the real connection.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusWriter) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	s.wroteHeader = true
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		s.wroteHeader = true
		f.Flush()
	}
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Mount serves h at path and hands every other request to app. Unlike
// wrapping app in a new ServeMux, the result still reports the matched
// pattern of app's own routes, so HTTPMetrics keeps labeling requests with
// "/follow" and "/timeline/" instead of one catch all route. It is how a
// service whose handler is built inside a library gains a /metrics endpoint
// without touching that library.
func Mount(app http.Handler, path string, h http.Handler) http.Handler {
	return &mounted{app: app, path: path, h: h}
}

type mounted struct {
	app  http.Handler
	path string
	h    http.Handler
}

func (m *mounted) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == m.path {
		m.h.ServeHTTP(w, r)
		return
	}
	m.app.ServeHTTP(w, r)
}

func (m *mounted) Handler(r *http.Request) (http.Handler, string) {
	if r.URL.Path == m.path {
		return m.h, m.path
	}
	if p, ok := m.app.(patterner); ok {
		return p.Handler(r)
	}
	return m.app, ""
}
