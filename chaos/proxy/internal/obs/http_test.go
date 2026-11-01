package obs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ok", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("fine")) })
	mux.HandleFunc("POST /limited", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTooManyRequests) })
	mux.HandleFunc("/user/", func(w http.ResponseWriter, r *http.Request) {}) // a prefix pattern, many paths
	mux.HandleFunc("/twice", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		w.WriteHeader(http.StatusInternalServerError) // ignored by net/http, so it must be ignored here too
	})
	return mux
}

func serve(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func has(t *testing.T, out, line string) {
	t.Helper()
	if !strings.Contains(out, line+"\n") {
		t.Errorf("missing %q in\n%s", line, out)
	}
}

func TestRequestsAreLabeledByMatchedRouteNotByPath(t *testing.T) {
	reg := NewRegistry()
	h := NewHTTPMetrics(reg).Wrap(newTestMux())

	serve(h, "GET", "/ok")
	serve(h, "GET", "/ok")
	serve(h, "POST", "/limited")
	serve(h, "GET", "/user/alice")
	serve(h, "GET", "/user/bob")
	serve(h, "GET", "/user/carol?x=1")

	out := scrape(reg)
	has(t, out, `http_requests_total{method="GET",route="GET /ok",status="200"} 2`)
	has(t, out, `http_requests_total{method="POST",route="POST /limited",status="429"} 1`)
	has(t, out, `http_requests_total{method="GET",route="/user/",status="200"} 3`)
	if strings.Contains(out, "alice") || strings.Contains(out, "bob") {
		t.Errorf("a path leaked into a label, which would make one series per user:\n%s", out)
	}
	has(t, out, `http_request_duration_seconds_count{method="GET",route="GET /ok"} 2`)
}

func TestUnmatchedRequestsShareOneRoute(t *testing.T) {
	reg := NewRegistry()
	h := NewHTTPMetrics(reg).Wrap(newTestMux())
	for _, p := range []string{"/nope", "/also-nope", "/x/y/z"} {
		serve(h, "GET", p)
	}
	has(t, scrape(reg), `http_requests_total{method="GET",route="unmatched",status="404"} 3`)
}

func TestAHandlerThatIsNotAMuxStillWorksUnderOneRoute(t *testing.T) {
	reg := NewRegistry()
	h := NewHTTPMetrics(reg).Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	serve(h, "GET", "/anything")
	has(t, scrape(reg), `http_requests_total{method="GET",route="unmatched",status="200"} 1`)
}

func TestStrangeMethodsShareOneLabelValue(t *testing.T) {
	reg := NewRegistry()
	h := NewHTTPMetrics(reg).Wrap(newTestMux())
	for _, m := range []string{"FOO", "BAR", "PROPFIND"} {
		serve(h, m, "/ok")
	}
	out := scrape(reg)
	if strings.Contains(out, "PROPFIND") || strings.Contains(out, `"FOO"`) {
		t.Errorf("a client-chosen method became a label value:\n%s", out)
	}
	has(t, out, `http_requests_total{method="OTHER",route="unmatched",status="405"} 3`)
}

func TestTheFirstStatusWins(t *testing.T) {
	reg := NewRegistry()
	h := NewHTTPMetrics(reg).Wrap(newTestMux())
	serve(h, "GET", "/twice")
	has(t, scrape(reg), `http_requests_total{method="GET",route="/twice",status="202"} 1`)
}

func TestScrapesAreNotCountedAsTraffic(t *testing.T) {
	reg := NewRegistry()
	m := NewHTTPMetrics(reg)
	mux := newTestMux()
	mux.Handle("/metrics", reg.Handler())
	h := m.Wrap(mux)
	serve(h, "GET", "/metrics")
	serve(h, "GET", "/metrics")
	if strings.Contains(scrape(reg), `route="/metrics"`) {
		t.Error("scraping /metrics should not appear as requests, or it inflates every rate by the scrape rate")
	}
}

func TestStreamingHandlersCanStillFlush(t *testing.T) {
	reg := NewRegistry()
	mux := http.NewServeMux()
	var viaAssertion, viaController bool
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		_, viaAssertion = w.(http.Flusher)
		viaController = http.NewResponseController(w).Flush() == nil
	})
	serve(NewHTTPMetrics(reg).Wrap(mux), "GET", "/stream")
	if !viaAssertion {
		t.Error("the wrapped writer must implement http.Flusher, or an event stream handler refuses to run")
	}
	if !viaController {
		t.Error("http.ResponseController must be able to reach the real writer through Unwrap")
	}
}

func TestAPanickingHandlerIsCountedAsA500AndStillPanics(t *testing.T) {
	reg := NewRegistry()
	mux := http.NewServeMux()
	mux.HandleFunc("/boom", func(w http.ResponseWriter, r *http.Request) { panic("handler bug") })
	h := NewHTTPMetrics(reg).Wrap(mux)

	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic must propagate so net/http can handle it as usual")
			}
		}()
		serve(h, "GET", "/boom")
	}()

	out := scrape(reg)
	has(t, out, `http_requests_total{method="GET",route="/boom",status="500"} 1`)
	has(t, out, "http_requests_in_flight 0")
}

func TestInFlightRequestsAreCountedWhileTheyRun(t *testing.T) {
	reg := NewRegistry()
	mux := http.NewServeMux()
	entered, release := make(chan struct{}), make(chan struct{})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
	})
	h := NewHTTPMetrics(reg).Wrap(mux)

	done := make(chan struct{})
	go func() { serve(h, "GET", "/slow"); close(done) }()
	<-entered
	has(t, scrape(reg), "http_requests_in_flight 1")
	close(release)
	<-done
	has(t, scrape(reg), "http_requests_in_flight 0")
}

func TestDurationIsMeasured(t *testing.T) {
	reg := NewRegistry()
	mux := http.NewServeMux()
	mux.HandleFunc("/nap", func(w http.ResponseWriter, r *http.Request) { time.Sleep(20 * time.Millisecond) })
	serve(NewHTTPMetrics(reg).Wrap(mux), "GET", "/nap")

	out := scrape(reg)
	has(t, out, `http_request_duration_seconds_bucket{method="GET",route="/nap",le="0.01"} 0`)
	has(t, out, `http_request_duration_seconds_bucket{method="GET",route="/nap",le="0.05"} 1`)
}

func TestSkippedRoutesAreCountedButNotTimed(t *testing.T) {
	reg := NewRegistry()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /stream", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("GET /normal", func(w http.ResponseWriter, r *http.Request) {})
	h := NewHTTPMetrics(reg, SkipDuration("GET /stream")).Wrap(mux)
	serve(h, "GET", "/stream")
	serve(h, "GET", "/normal")

	out := scrape(reg)
	has(t, out, `http_requests_total{method="GET",route="GET /stream",status="200"} 1`)
	if strings.Contains(out, `http_request_duration_seconds_count{method="GET",route="GET /stream"}`) {
		t.Error("a long lived stream should stay out of the latency histogram")
	}
	has(t, out, `http_request_duration_seconds_count{method="GET",route="GET /normal"} 1`)
}

// deadlineWriter stands in for the real connection, which supports per
// response deadlines. The recorder used elsewhere in these tests does not.
type deadlineWriter struct {
	*httptest.ResponseRecorder
	deadlineSet bool
}

func (d *deadlineWriter) SetWriteDeadline(time.Time) error {
	d.deadlineSet = true
	return nil
}

func TestResponseControllerFeaturesReachTheRealWriter(t *testing.T) {
	reg := NewRegistry()
	mux := http.NewServeMux()
	var err error
	mux.HandleFunc("/deadline", func(w http.ResponseWriter, r *http.Request) {
		err = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Minute))
	})
	real := &deadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	NewHTTPMetrics(reg).Wrap(mux).ServeHTTP(real, httptest.NewRequest("GET", "/deadline", nil))

	if err != nil || !real.deadlineSet {
		t.Fatalf("a handler could not set a write deadline through the wrapper (err=%v), so Unwrap is not doing its job", err)
	}
}

func TestMountServesMetricsAndKeepsTheAppsRouteLabels(t *testing.T) {
	reg := NewRegistry()
	app := http.NewServeMux()
	app.HandleFunc("/follow", func(w http.ResponseWriter, r *http.Request) {})
	app.HandleFunc("/timeline/", func(w http.ResponseWriter, r *http.Request) {})
	h := NewHTTPMetrics(reg).Wrap(Mount(app, "/metrics", reg.Handler()))

	serve(h, "POST", "/follow")
	serve(h, "GET", "/timeline/alice")
	serve(h, "GET", "/timeline/bob")
	scraped := serve(h, "GET", "/metrics")

	if !strings.Contains(scraped.Body.String(), "# TYPE http_requests_total counter") {
		t.Fatalf("Mount should serve the metrics page, got:\n%s", scraped.Body.String())
	}
	out := scrape(reg)
	has(t, out, `http_requests_total{method="POST",route="/follow",status="200"} 1`)
	has(t, out, `http_requests_total{method="GET",route="/timeline/",status="200"} 2`)
	if strings.Contains(out, `route="/"`) {
		t.Errorf("the app's routes collapsed into one catch all route:\n%s", out)
	}
}

func TestMountPassesUnmatchedRequestsToTheApp(t *testing.T) {
	reg := NewRegistry()
	app := http.NewServeMux()
	h := NewHTTPMetrics(reg).Wrap(Mount(app, "/metrics", reg.Handler()))
	if rec := serve(h, "GET", "/nothing"); rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want the app's 404", rec.Code)
	}
	has(t, scrape(reg), `http_requests_total{method="GET",route="unmatched",status="404"} 1`)
}
