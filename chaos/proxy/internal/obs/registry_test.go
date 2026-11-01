package obs

import (
	"fmt"
	"math"
	"net/http/httptest"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func scrape(r *Registry) string {
	var b strings.Builder
	_, _ = r.WriteTo(&b)
	return b.String()
}

func mustPanic(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: expected a panic", name)
		}
	}()
	f()
}

func TestCounterGaugeAndHistogramExposition(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounter("jobs_total", "Jobs done.")
	g := r.NewGauge("queue_depth", "Jobs waiting.")
	h := r.NewHistogram("job_seconds", "Job time.", []float64{0.1, 1})
	c.Inc()
	c.Add(2.5)
	g.Set(7)
	g.Dec()
	h.Observe(0.05)
	h.Observe(0.1) // exactly on a bound: belongs in that bucket
	h.Observe(0.5)
	h.Observe(30)

	want := `# HELP job_seconds Job time.
# TYPE job_seconds histogram
job_seconds_bucket{le="0.1"} 2
job_seconds_bucket{le="1"} 3
job_seconds_bucket{le="+Inf"} 4
job_seconds_sum 30.65
job_seconds_count 4
# HELP jobs_total Jobs done.
# TYPE jobs_total counter
jobs_total 3.5
# HELP obs_collector_errors_total Scrape time collectors that panicked and were skipped.
# TYPE obs_collector_errors_total counter
# HELP queue_depth Jobs waiting.
# TYPE queue_depth gauge
queue_depth 6
`
	if got := scrape(r); got != want {
		t.Fatalf("exposition differs.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestLabeledMetricsAreSortedAndEscaped(t *testing.T) {
	r := NewRegistry()
	cv := r.NewCounterVec("req_total", "Requests with a \\ and a\nnewline in the help.", "path", "code")
	cv.WithLabelValues("/b", "200").Inc()
	cv.WithLabelValues("/a", "500").Add(2)
	cv.WithLabelValues("/a", "200").Inc()
	cv.WithLabelValues(`say "hi"\`+"\n", "200").Inc()

	out := scrape(r)
	wantLines := []string{
		`# HELP req_total Requests with a \\ and a\nnewline in the help.`,
		`req_total{path="/a",code="200"} 1`,
		`req_total{path="/a",code="500"} 2`,
		`req_total{path="/b",code="200"} 1`,
		`req_total{path="say \"hi\"\\\n",code="200"} 1`,
	}
	last := -1
	for _, l := range wantLines {
		i := strings.Index(out, l+"\n")
		if i < 0 {
			t.Fatalf("missing line %q in:\n%s", l, out)
		}
		if i < last {
			t.Fatalf("line %q is out of order in:\n%s", l, out)
		}
		last = i
	}
}

func TestSpecialFloatValues(t *testing.T) {
	r := NewRegistry()
	r.NewGauge("a", "x").Set(math.Inf(1))
	r.NewGauge("b", "x").Set(math.Inf(-1))
	r.NewGauge("c", "x").Set(math.NaN())
	out := scrape(r)
	for _, want := range []string{"a +Inf\n", "b -Inf\n", "c NaN\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestNamingMistakesPanicAtRegistration(t *testing.T) {
	r := NewRegistry()
	mustPanic(t, "counter without _total", func() { r.NewCounter("jobs", "x") })
	mustPanic(t, "counter vec without _total", func() { r.NewCounterVec("jobs", "x", "a") })
	mustPanic(t, "invalid metric name", func() { r.NewGauge("1bad-name", "x") })
	mustPanic(t, "invalid label name", func() { r.NewGaugeVec("g", "x", "bad-label") })
	mustPanic(t, "reserved le label", func() { r.NewGaugeVec("g2", "x", "le") })
	mustPanic(t, "reserved __ prefix", func() { r.NewGaugeVec("g3", "x", "__name") })
	mustPanic(t, "duplicate label", func() { r.NewGaugeVec("g4", "x", "a", "a") })
	r.NewGauge("dup", "x")
	mustPanic(t, "duplicate metric", func() { r.NewGauge("dup", "x") })
	mustPanic(t, "no buckets", func() { r.NewHistogram("h1", "x", nil) })
	mustPanic(t, "unsorted buckets", func() { r.NewHistogram("h2", "x", []float64{1, 0.5}) })
	mustPanic(t, "infinite bucket", func() { r.NewHistogram("h3", "x", []float64{1, math.Inf(1)}) })
	mustPanic(t, "wrong label value count", func() { r.NewCounterVec("w_total", "x", "a").WithLabelValues("1", "2") })
}

func TestACounterCannotGoDown(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounter("x_total", "x")
	mustPanic(t, "negative add", func() { c.Add(-1) })
	c.Add(0)
}

func TestHistogramEdgeValues(t *testing.T) {
	r := NewRegistry()
	h := r.NewHistogram("h", "x", []float64{1, 2})
	h.Observe(-5) // below every bound
	h.Observe(math.Inf(1))
	out := scrape(r)
	for _, want := range []string{`h_bucket{le="1"} 1`, `h_bucket{le="2"} 1`, `h_bucket{le="+Inf"} 2`, "h_count 2"} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestLabeledHistogramKeepsLabelsBeforeLe(t *testing.T) {
	r := NewRegistry()
	hv := r.NewHistogramVec("lat_seconds", "x", []float64{1}, "route")
	hv.WithLabelValues("/a").Observe(0.5)
	out := scrape(r)
	for _, want := range []string{`lat_seconds_bucket{route="/a",le="1"} 1`, `lat_seconds_bucket{route="/a",le="+Inf"} 1`, `lat_seconds_sum{route="/a"} 0.5`, `lat_seconds_count{route="/a"} 1`} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestSeriesCapFoldsNewCombinationsIntoOverflow(t *testing.T) {
	r := NewRegistry()
	cv := r.NewCounterVec("user_hits_total", "x", "user")
	for i := 0; i < MaxSeries+100; i++ {
		cv.WithLabelValues(fmt.Sprintf("u%d", i)).Inc()
	}
	cv.WithLabelValues("u0").Inc() // an existing series keeps counting after the cap

	out := scrape(r)
	series := strings.Count(out, "user_hits_total{")
	if series != MaxSeries+1 {
		t.Fatalf("expected %d series plus one overflow series, got %d", MaxSeries, series)
	}
	if !strings.Contains(out, `user_hits_total{user="overflow"} 100`+"\n") {
		t.Errorf("the 100 extra users should be folded into one overflow series:\n%s", tail(out))
	}
	if !strings.Contains(out, `user_hits_total{user="u0"} 2`+"\n") {
		t.Errorf("an existing series must keep counting")
	}
}

func tail(s string) string {
	if len(s) > 400 {
		return s[len(s)-400:]
	}
	return s
}

func TestScrapeTimeValuesAreReadOnEveryScrape(t *testing.T) {
	r := NewRegistry()
	n := 0.0
	r.NewGaugeFunc("live_value", "x", func() float64 { n++; return n })
	r.NewCounterFunc("seen_total", "x", func() float64 { return 42 })
	r.NewGaugeFuncVec("lag", "x", []string{"partition"}, func() []Sample {
		return []Sample{{Labels: []string{"2"}, Value: 5}, {Labels: []string{"0"}, Value: 9}}
	})
	if !strings.Contains(scrape(r), "live_value 1\n") || !strings.Contains(scrape(r), "live_value 2\n") {
		t.Fatal("a scrape time function must be called again on each scrape")
	}
	out := scrape(r)
	if strings.Index(out, `lag{partition="0"} 9`) > strings.Index(out, `lag{partition="2"} 5`) {
		t.Errorf("labeled samples should come out sorted:\n%s", out)
	}
	if !strings.Contains(out, "# TYPE seen_total counter") {
		t.Errorf("CounterFunc must be typed as a counter")
	}
}

func TestAPanickingCollectorIsSkippedAndCounted(t *testing.T) {
	r := NewRegistry()
	r.NewGauge("healthy", "x").Set(1)
	r.NewGaugeFunc("broken", "x", func() float64 { panic("boom") })

	errorsSeen := func(out string) float64 {
		m := regexp.MustCompile(`obs_collector_errors_total\{collector="broken"\} (\S+)`).FindStringSubmatch(out)
		if m == nil {
			return 0
		}
		var v float64
		fmt.Sscan(m[1], &v)
		return v
	}

	first := scrape(r)
	if strings.Contains(first, "# TYPE broken") || strings.Contains(first, "\nbroken ") {
		t.Errorf("a collector that panicked should leave no partial output:\n%s", first)
	}
	if !strings.Contains(first, "healthy 1\n") {
		t.Errorf("the rest of the page must still be served:\n%s", first)
	}
	second := scrape(r)
	if errorsSeen(second) <= errorsSeen(first) || errorsSeen(second) == 0 {
		t.Errorf("each failed scrape should be counted, saw %v then %v", errorsSeen(first), errorsSeen(second))
	}
}

var sampleLine = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*(\{([a-zA-Z_][a-zA-Z0-9_]*="([^"\\]|\\.)*"(,|\}))+)? (-?[0-9.e+-]+|\+Inf|-Inf|NaN)$`)

func TestEveryLineIsValidTextFormat(t *testing.T) {
	r := NewRegistry()
	r.NewCounterVec("a_total", "x", "l").WithLabelValues("v\"\n\\").Inc()
	r.NewHistogramVec("b_seconds", "x", DefBuckets, "l").WithLabelValues("v").Observe(0.2)
	RegisterRuntime(r, "test")
	NewHTTPMetrics(r)

	families := map[string]int{}
	for _, line := range strings.Split(strings.TrimSuffix(scrape(r), "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "# HELP "):
		case strings.HasPrefix(line, "# TYPE "):
			families[strings.Fields(line)[2]]++
		default:
			if !sampleLine.MatchString(line) {
				t.Errorf("not a valid sample line: %q", line)
			}
		}
	}
	for name, n := range families {
		if n != 1 {
			t.Errorf("family %s has %d TYPE lines, want 1", name, n)
		}
	}
}

func TestConcurrentUpdatesAndScrapesAreExact(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounter("n_total", "x")
	g := r.NewGauge("g", "x")
	cv := r.NewCounterVec("v_total", "x", "k")
	h := r.NewHistogram("h", "x", []float64{1})

	const workers, per = 40, 500
	var wg sync.WaitGroup
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				scrape(r)
			}
		}
	}()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				c.Inc()
				g.Add(2)
				cv.WithLabelValues(fmt.Sprint(w % 5)).Inc()
				h.Observe(0.5)
			}
		}(w)
	}
	wg.Wait()
	close(stop)

	out := scrape(r)
	for _, want := range []string{
		fmt.Sprintf("n_total %d\n", workers*per),
		fmt.Sprintf("g %d\n", workers*per*2),
		fmt.Sprintf("h_count %d\n", workers*per),
		fmt.Sprintf(`h_bucket{le="1"} %d`+"\n", workers*per),
		fmt.Sprintf(`v_total{k="0"} %d`+"\n", workers/5*per),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lost updates, missing %q in\n%s", want, out)
		}
	}
}

func TestHandlerServesTheTextFormat(t *testing.T) {
	r := NewRegistry()
	r.NewGauge("up_thing", "x").Set(1)
	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; version=0.0.4; charset=utf-8" {
		t.Errorf("content type %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "up_thing 1\n") {
		t.Errorf("body %q", rec.Body.String())
	}
}

func TestRuntimeMetricsArePublished(t *testing.T) {
	r := NewRegistry()
	RegisterRuntime(r, "demo")
	out := scrape(r)
	for _, want := range []string{"go_goroutines ", "go_memstats_alloc_bytes ", "go_gc_cycles_total ", "process_start_time_seconds ", `faultline_build_info{service="demo",go_version="go`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

// Lost updates only appear when goroutines really run at the same moment, so
// this releases them together and gives the scheduler plenty of threads. On a
// single core it catches a broken add less often than on a multi core
// machine, which is where CI runs it.
func TestNoUpdateIsLostWhenManyGoroutinesStartTogether(t *testing.T) {
	prev := runtime.GOMAXPROCS(8)
	defer runtime.GOMAXPROCS(prev)

	r := NewRegistry()
	g := r.NewGauge("shared", "x")
	const workers, per = 64, 20000
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < per; i++ {
				g.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := g.v.Load(); got != workers*per {
		t.Fatalf("lost %v of %d updates", workers*per-got, workers*per)
	}
}
