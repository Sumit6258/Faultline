package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	urlshortener "github.com/Sumit6258/Faultline/systems/url-shortener"
	"github.com/Sumit6258/Faultline/systems/url-shortener/internal/idgen"
	"github.com/Sumit6258/Faultline/systems/url-shortener/internal/store"
)

func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("/metrics returned %d", rec.Code)
	}
	b, _ := io.ReadAll(rec.Body)
	return string(b)
}

func TestMetricsReportTheServicesOwnCacheCounters(t *testing.T) {
	svc := urlshortener.New(&idgen.Counter{}, store.NewMemory(), 10, 20, 5, 100)
	defer svc.Stop()
	h := observe(http.NewServeMux(), svc)

	code, err := svc.Shorten("client", "https://example.com/a")
	if err != nil {
		t.Fatal(err)
	}
	svc.Resolve(code)
	svc.Resolve(code)
	svc.Resolve("nosuchcode")

	stats := svc.Stats()
	if stats.CacheHits+stats.CacheMisses == 0 {
		t.Fatal("the test needs some cache activity to mean anything")
	}
	out := scrape(t, h)
	for _, want := range []string{
		fmt.Sprintf("urlshortener_cache_hits_total %d\n", stats.CacheHits),
		fmt.Sprintf("urlshortener_cache_misses_total %d\n", stats.CacheMisses),
		"# TYPE http_requests_total counter",
		"go_goroutines ",
		`faultline_build_info{service="url-shortener"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestRequestsAreLabeledByRouteAndRateLimitedCreationsShowAs429(t *testing.T) {
	svc := urlshortener.New(&idgen.Counter{}, store.NewMemory(), 10, 20, 5, 100)
	defer svc.Stop()
	mux := http.NewServeMux()
	mux.HandleFunc("/shorten", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTooManyRequests) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {})
	h := observe(mux, svc)

	for _, p := range []string{"/shorten", "/abc", "/def"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", p, nil))
	}
	out := scrape(t, h)
	for _, want := range []string{
		`http_requests_total{method="GET",route="/shorten",status="429"} 1`,
		`http_requests_total{method="GET",route="/",status="200"} 2`,
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "abc") || strings.Contains(out, "def") {
		t.Error("a short code leaked into a label, one series per link would follow")
	}
}
