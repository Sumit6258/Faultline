package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAllowedAndRejectedRequestsAreSeparateSeriesAndTheAlgorithmIsPublished(t *testing.T) {
	calls := 0
	app := http.NewServeMux()
	app.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls > 2 {
			w.WriteHeader(http.StatusTooManyRequests)
		}
	})
	h := observe(app, "sliding-window")
	for i := 0; i < 5; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	b, _ := io.ReadAll(rec.Body)
	out := string(b)
	for _, want := range []string{
		`http_requests_total{method="GET",route="/",status="200"} 2` + "\n",
		`http_requests_total{method="GET",route="/",status="429"} 3` + "\n",
		`ratelimit_algorithm_info{algorithm="sliding-window"} 1` + "\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}
