package obs

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// What the kit costs. The numbers that matter are the middleware's, because
// that runs on every request of every service.

func BenchmarkCounterInc(b *testing.B) {
	c := NewRegistry().NewCounter("bench_total", "x")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.Inc()
	}
}

func BenchmarkCounterIncParallel(b *testing.B) {
	c := NewRegistry().NewCounter("bench_total", "x")
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Inc()
		}
	})
}

func BenchmarkCounterVecLookupAndInc(b *testing.B) {
	cv := NewRegistry().NewCounterVec("bench_total", "x", "method", "route", "status")
	cv.WithLabelValues("GET", "/timeline/", "200").Inc() // the series already exists, as it does for all but a service's first request
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cv.WithLabelValues("GET", "/timeline/", "200").Inc()
	}
}

func BenchmarkHistogramObserve(b *testing.B) {
	h := NewRegistry().NewHistogram("bench_seconds", "x", DefBuckets)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		h.Observe(0.003)
	}
}

// nopWriter swallows the response, so the benchmark measures the handler and
// the middleware, not a recorder's buffer.
type nopWriter struct{ h http.Header }

func (w *nopWriter) Header() http.Header         { return w.h }
func (w *nopWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *nopWriter) WriteHeader(int)             {}

func benchMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /timeline/{user}", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	return mux
}

// BenchmarkHandlerBare is the same handler with no instrumentation, so the
// difference to BenchmarkHandlerInstrumented is what instrumenting costs.
func BenchmarkHandlerBare(b *testing.B) {
	h := http.Handler(benchMux())
	req := httptest.NewRequest("GET", "/timeline/alice", nil)
	w := &nopWriter{h: http.Header{}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(w, req)
	}
}

func BenchmarkHandlerInstrumented(b *testing.B) {
	h := NewHTTPMetrics(NewRegistry()).Wrap(benchMux())
	req := httptest.NewRequest("GET", "/timeline/alice", nil)
	w := &nopWriter{h: http.Header{}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(w, req)
	}
}

func BenchmarkHandlerInstrumentedParallel(b *testing.B) {
	h := NewHTTPMetrics(NewRegistry()).Wrap(benchMux())
	req := httptest.NewRequest("GET", "/timeline/alice", nil)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		w := &nopWriter{h: http.Header{}}
		for pb.Next() {
			h.ServeHTTP(w, req)
		}
	})
}

// BenchmarkScrape is the cost of one Prometheus scrape, as the number of
// series grows. It happens every few seconds, not per request.
func BenchmarkScrape(b *testing.B) {
	for _, series := range []int{10, 100, 500} {
		b.Run(fmt.Sprintf("series=%d", series), func(b *testing.B) {
			r := NewRegistry()
			cv := r.NewCounterVec("bench_total", "x", "route")
			for i := 0; i < series; i++ {
				cv.WithLabelValues(fmt.Sprintf("/r%d", i)).Inc()
			}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				r.WriteTo(io.Discard)
			}
		})
	}
}
