// Package obs is a small metrics library that speaks the Prometheus text
// exposition format. It has no dependencies, so every Faultline module can
// carry its own copy and stay self-contained. The canonical source lives in
// observability/kit/go/obs, and scripts/sync-obs.sh copies it into each
// service. CI fails if a copy has drifted.
//
// It is deliberately small: counters, gauges, histograms, labeled versions of
// each, and values computed at scrape time. Naming follows Prometheus
// conventions and is enforced where a mistake would be silent: counters must
// end in _total, and a labeled metric caps its series count so an unbounded
// label value cannot take the process down.
package obs

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// MaxSeries is how many distinct label combinations one labeled metric will
// track. Past that, new combinations are folded into a single series whose
// label values are all "overflow". Every label value in this repository is
// meant to come from a small fixed set, and this is the backstop for the day
// one is not, such as a user id slipping into a label.
const MaxSeries = 500

var (
	metricName = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
	labelName  = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

type kind int

const (
	kindCounter kind = iota
	kindGauge
	kindHistogram
)

func (k kind) String() string { return [...]string{"counter", "gauge", "histogram"}[k] }

type collector interface {
	metricName() string
	help() string
	kind() kind
	write(w io.Writer)
}

// Registry holds metrics and serves them.
type Registry struct {
	mu         sync.RWMutex
	collectors map[string]collector
	errors     *CounterVec
}

func NewRegistry() *Registry {
	r := &Registry{collectors: map[string]collector{}}
	r.errors = r.NewCounterVec("obs_collector_errors_total", "Scrape time collectors that panicked and were skipped.", "collector")
	return r
}

func (r *Registry) register(c collector) {
	if !metricName.MatchString(c.metricName()) {
		panic(fmt.Sprintf("obs: invalid metric name %q", c.metricName()))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.collectors[c.metricName()]; dup {
		panic(fmt.Sprintf("obs: metric %q registered twice", c.metricName()))
	}
	r.collectors[c.metricName()] = c
}

func checkLabels(names []string) {
	seen := map[string]bool{}
	for _, n := range names {
		if !labelName.MatchString(n) || strings.HasPrefix(n, "__") || n == "le" {
			panic(fmt.Sprintf("obs: invalid label name %q", n))
		}
		if seen[n] {
			panic(fmt.Sprintf("obs: duplicate label name %q", n))
		}
		seen[n] = true
	}
}

func checkCounterName(name string) {
	if !strings.HasSuffix(name, "_total") {
		panic(fmt.Sprintf("obs: counter %q must end in _total", name))
	}
}

// Handler serves the registry in the Prometheus text format.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = r.WriteTo(w)
	})
}

// WriteTo writes every metric, sorted by name, so output is deterministic.
func (r *Registry) WriteTo(w io.Writer) (int64, error) {
	r.mu.RLock()
	names := make([]string, 0, len(r.collectors))
	for n := range r.collectors {
		names = append(names, n)
	}
	sort.Strings(names)
	cs := make([]collector, len(names))
	for i, n := range names {
		cs[i] = r.collectors[n]
	}
	r.mu.RUnlock()

	cw := &countingWriter{w: w}
	for _, c := range cs {
		r.writeOne(cw, c)
	}
	return cw.n, cw.err
}

// writeOne isolates one collector. A scrape time function that panics is
// skipped and counted, and the rest of the page is still served.
func (r *Registry) writeOne(w io.Writer, c collector) {
	var buf strings.Builder
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				buf.Reset()
				r.errors.WithLabelValues(c.metricName()).Inc()
			}
		}()
		fmt.Fprintf(&buf, "# HELP %s %s\n# TYPE %s %s\n", c.metricName(), escapeHelp(c.help()), c.metricName(), c.kind())
		c.write(&buf)
	}()
	_, _ = io.WriteString(w, buf.String())
}

type countingWriter struct {
	w   io.Writer
	n   int64
	err error
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	if err != nil && c.err == nil {
		c.err = err
	}
	return n, err
}

// atomicFloat is a float64 that can be added to from many goroutines.
type atomicFloat struct{ bits atomic.Uint64 }

func (a *atomicFloat) Load() float64   { return math.Float64frombits(a.bits.Load()) }
func (a *atomicFloat) Store(v float64) { a.bits.Store(math.Float64bits(v)) }
func (a *atomicFloat) Add(delta float64) {
	for {
		old := a.bits.Load()
		if a.bits.CompareAndSwap(old, math.Float64bits(math.Float64frombits(old)+delta)) {
			return
		}
	}
}

// ---------------------------------------------------------------- counters

type Counter struct{ v atomicFloat }

func (c *Counter) Inc() { c.v.Add(1) }

// Add panics on a negative value, because a counter that goes down breaks
// every rate() computed from it.
func (c *Counter) Add(v float64) {
	if v < 0 {
		panic("obs: counter cannot decrease")
	}
	c.v.Add(v)
}

type counterMetric struct {
	name, helpText string
	c              *Counter
}

func (m *counterMetric) metricName() string { return m.name }
func (m *counterMetric) help() string       { return m.helpText }
func (m *counterMetric) kind() kind         { return kindCounter }
func (m *counterMetric) write(w io.Writer)  { writeSample(w, m.name, nil, nil, m.c.v.Load()) }

func (r *Registry) NewCounter(name, help string) *Counter {
	checkCounterName(name)
	c := &Counter{}
	r.register(&counterMetric{name, help, c})
	return c
}

// ------------------------------------------------------------------ gauges

type Gauge struct{ v atomicFloat }

func (g *Gauge) Set(v float64) { g.v.Store(v) }
func (g *Gauge) Add(v float64) { g.v.Add(v) }
func (g *Gauge) Inc()          { g.v.Add(1) }
func (g *Gauge) Dec()          { g.v.Add(-1) }

type gaugeMetric struct {
	name, helpText string
	g              *Gauge
}

func (m *gaugeMetric) metricName() string { return m.name }
func (m *gaugeMetric) help() string       { return m.helpText }
func (m *gaugeMetric) kind() kind         { return kindGauge }
func (m *gaugeMetric) write(w io.Writer)  { writeSample(w, m.name, nil, nil, m.g.v.Load()) }

func (r *Registry) NewGauge(name, help string) *Gauge {
	g := &Gauge{}
	r.register(&gaugeMetric{name, help, g})
	return g
}

// ----------------------------------------------------------- labeled vecs

// vec is the shared machinery for labeled metrics: it hands out one child
// per distinct combination of label values, up to MaxSeries.
type vec[T any] struct {
	name, helpText string
	k              kind
	labels         []string
	newChild       func() *T
	mu             sync.RWMutex
	children       map[string]*vecChild[T]
	overflow       *vecChild[T]
}

type vecChild[T any] struct {
	values []string
	child  *T
}

func newVec[T any](name, help string, k kind, labels []string, newChild func() *T) *vec[T] {
	checkLabels(labels)
	return &vec[T]{name: name, helpText: help, k: k, labels: labels, newChild: newChild, children: map[string]*vecChild[T]{}}
}

func (v *vec[T]) with(values []string) *T {
	if len(values) != len(v.labels) {
		panic(fmt.Sprintf("obs: %s expects %d label values, got %d", v.name, len(v.labels), len(values)))
	}
	key := strings.Join(values, "\xff")
	v.mu.RLock()
	c, ok := v.children[key]
	v.mu.RUnlock()
	if ok {
		return c.child
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if c, ok := v.children[key]; ok {
		return c.child
	}
	if len(v.children) >= MaxSeries {
		if v.overflow == nil {
			over := make([]string, len(v.labels))
			for i := range over {
				over[i] = "overflow"
			}
			v.overflow = &vecChild[T]{values: over, child: v.newChild()}
		}
		return v.overflow.child
	}
	c = &vecChild[T]{values: append([]string(nil), values...), child: v.newChild()}
	v.children[key] = c
	return c.child
}

// sorted returns children ordered by label values, with the overflow series last.
func (v *vec[T]) sorted() []*vecChild[T] {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make([]*vecChild[T], 0, len(v.children)+1)
	for _, c := range v.children {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return strings.Join(out[i].values, "\xff") < strings.Join(out[j].values, "\xff") })
	if v.overflow != nil {
		out = append(out, v.overflow)
	}
	return out
}

func (v *vec[T]) metricName() string { return v.name }
func (v *vec[T]) help() string       { return v.helpText }
func (v *vec[T]) kind() kind         { return v.k }

type CounterVec struct{ *vec[Counter] }

func (r *Registry) NewCounterVec(name, help string, labels ...string) *CounterVec {
	checkCounterName(name)
	cv := &CounterVec{newVec(name, help, kindCounter, labels, func() *Counter { return &Counter{} })}
	r.register(cv)
	return cv
}

func (cv *CounterVec) WithLabelValues(values ...string) *Counter { return cv.with(values) }

func (cv *CounterVec) write(w io.Writer) {
	for _, c := range cv.sorted() {
		writeSample(w, cv.name, cv.labels, c.values, c.child.v.Load())
	}
}

type GaugeVec struct{ *vec[Gauge] }

func (r *Registry) NewGaugeVec(name, help string, labels ...string) *GaugeVec {
	gv := &GaugeVec{newVec(name, help, kindGauge, labels, func() *Gauge { return &Gauge{} })}
	r.register(gv)
	return gv
}

func (gv *GaugeVec) WithLabelValues(values ...string) *Gauge { return gv.with(values) }

func (gv *GaugeVec) write(w io.Writer) {
	for _, c := range gv.sorted() {
		writeSample(w, gv.name, gv.labels, c.values, c.child.v.Load())
	}
}

// -------------------------------------------------------------- histograms

// DefBuckets suit services whose handlers run in microseconds to a few
// seconds, which is the range everything in this repository lives in.
var DefBuckets = []float64{0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5}

type Histogram struct {
	bounds []float64
	counts []atomic.Uint64 // per bucket, not cumulative, last slot is +Inf
	sum    atomicFloat
	total  atomic.Uint64
}

func newHistogram(bounds []float64) *Histogram {
	return &Histogram{bounds: bounds, counts: make([]atomic.Uint64, len(bounds)+1)}
}

// Observe records one value. A value equal to a bound falls in that bucket,
// as the "le" (less than or equal) label promises.
func (h *Histogram) Observe(v float64) {
	h.counts[sort.SearchFloat64s(h.bounds, v)].Add(1)
	h.sum.Add(v)
	h.total.Add(1)
}

func checkBuckets(b []float64) []float64 {
	if len(b) == 0 {
		panic("obs: histogram needs at least one bucket")
	}
	for i, v := range b {
		if math.IsNaN(v) || math.IsInf(v, 0) || (i > 0 && v <= b[i-1]) {
			panic("obs: histogram buckets must be finite and strictly increasing")
		}
	}
	return append([]float64(nil), b...)
}

func (h *Histogram) write(w io.Writer, name string, labels, values []string) {
	var cumulative uint64
	for i, bound := range h.bounds {
		cumulative += h.counts[i].Load()
		writeSample(w, name+"_bucket", append(append([]string(nil), labels...), "le"), append(append([]string(nil), values...), formatFloat(bound)), float64(cumulative))
	}
	cumulative += h.counts[len(h.bounds)].Load()
	writeSample(w, name+"_bucket", append(append([]string(nil), labels...), "le"), append(append([]string(nil), values...), "+Inf"), float64(cumulative))
	writeSample(w, name+"_sum", labels, values, h.sum.Load())
	writeSample(w, name+"_count", labels, values, float64(h.total.Load()))
}

type histogramMetric struct {
	name, helpText string
	h              *Histogram
}

func (m *histogramMetric) metricName() string { return m.name }
func (m *histogramMetric) help() string       { return m.helpText }
func (m *histogramMetric) kind() kind         { return kindHistogram }
func (m *histogramMetric) write(w io.Writer)  { m.h.write(w, m.name, nil, nil) }

func (r *Registry) NewHistogram(name, help string, buckets []float64) *Histogram {
	h := newHistogram(checkBuckets(buckets))
	r.register(&histogramMetric{name, help, h})
	return h
}

type HistogramVec struct{ *vec[Histogram] }

func (r *Registry) NewHistogramVec(name, help string, buckets []float64, labels ...string) *HistogramVec {
	b := checkBuckets(buckets)
	hv := &HistogramVec{newVec(name, help, kindHistogram, labels, func() *Histogram { return newHistogram(b) })}
	r.register(hv)
	return hv
}

func (hv *HistogramVec) WithLabelValues(values ...string) *Histogram { return hv.with(values) }

func (hv *HistogramVec) write(w io.Writer) {
	for _, c := range hv.sorted() {
		c.child.write(w, hv.name, hv.labels, c.values)
	}
}

// ---------------------------------------------------- scrape time values

// Sample is one labeled value produced by a scrape time function.
type Sample struct {
	Labels []string
	Value  float64
}

type funcMetric struct {
	name, helpText string
	k              kind
	labels         []string
	fn             func() []Sample
}

func (m *funcMetric) metricName() string { return m.name }
func (m *funcMetric) help() string       { return m.helpText }
func (m *funcMetric) kind() kind         { return m.k }
func (m *funcMetric) write(w io.Writer) {
	samples := m.fn()
	sort.Slice(samples, func(i, j int) bool {
		return strings.Join(samples[i].Labels, "\xff") < strings.Join(samples[j].Labels, "\xff")
	})
	for _, s := range samples {
		if len(s.Labels) != len(m.labels) {
			panic(fmt.Sprintf("obs: %s sample has %d label values, want %d", m.name, len(s.Labels), len(m.labels)))
		}
		writeSample(w, m.name, m.labels, s.Labels, s.Value)
	}
}

func single(fn func() float64) func() []Sample {
	return func() []Sample { return []Sample{{Value: fn()}} }
}

// NewGaugeFunc exposes a value read at scrape time. Use it for state that
// already lives somewhere else, such as a queue length, so the service code
// does not need to be touched to publish it.
func (r *Registry) NewGaugeFunc(name, help string, fn func() float64) {
	r.register(&funcMetric{name: name, helpText: help, k: kindGauge, fn: single(fn)})
}

// NewCounterFunc exposes a monotonically increasing value read at scrape time.
func (r *Registry) NewCounterFunc(name, help string, fn func() float64) {
	checkCounterName(name)
	r.register(&funcMetric{name: name, helpText: help, k: kindCounter, fn: single(fn)})
}

func (r *Registry) NewGaugeFuncVec(name, help string, labels []string, fn func() []Sample) {
	checkLabels(labels)
	r.register(&funcMetric{name: name, helpText: help, k: kindGauge, labels: labels, fn: fn})
}

// ------------------------------------------------------------ text format

func writeSample(w io.Writer, name string, labels, values []string, v float64) {
	var b strings.Builder
	b.WriteString(name)
	if len(labels) > 0 {
		b.WriteByte('{')
		for i, l := range labels {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(l)
			b.WriteString(`="`)
			b.WriteString(escapeLabelValue(values[i]))
			b.WriteByte('"')
		}
		b.WriteByte('}')
	}
	b.WriteByte(' ')
	b.WriteString(formatFloat(v))
	b.WriteByte('\n')
	_, _ = io.WriteString(w, b.String())
}

func formatFloat(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

var (
	helpEscaper  = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
	labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
)

func escapeHelp(s string) string       { return helpEscaper.Replace(s) }
func escapeLabelValue(s string) string { return labelEscaper.Replace(s) }
