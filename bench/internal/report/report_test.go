package report

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Sumit6258/Faultline/bench/internal/model"
)

func env() model.Environment {
	return model.Environment{GOOS: "linux", GOARCH: "amd64", CPU: "Xeon @ 2.10GHz", NumCPU: 1, GoVersion: "go1.22.2", Commit: "abc1234",
		Date: "2026-11-01T10:00:00Z", Count: 5, BenchTime: "200ms"}
}

func ser(module, name string, ns []float64, allocs, bytes float64) model.Series {
	s := model.Series{Module: module, Package: "example.com/" + module, Name: name, Procs: 1, Samples: map[string][]float64{"ns/op": ns}}
	if allocs >= 0 {
		a, b := make([]float64, len(ns)), make([]float64, len(ns))
		for i := range ns {
			a[i], b[i] = allocs, bytes
		}
		s.Samples["allocs/op"], s.Samples["B/op"] = a, b
	}
	return s
}

func file(e model.Environment, ss ...model.Series) model.File {
	return model.File{Version: model.Version, Env: e, Series: ss}
}

func TestFormatTimePicksAUnitAndThreeSignificantDigits(t *testing.T) {
	for ns, want := range map[float64]string{14.53: "14.5 ns", 0.33: "0.33 ns", 138.1: "138 ns", 1020: "1.02 us", 82500: "82.5 us", 755900000: "756 ms", 2.5e9: "2.50 s"} {
		if got := FormatTime(ns); got != want {
			t.Errorf("FormatTime(%v) = %q, want %q", ns, got, want)
		}
	}
}

func TestDisplayNameDropsThePrefixAndShowsASubPackage(t *testing.T) {
	root := model.Series{Module: "labs/caching", Package: "github.com/x/Faultline/labs/caching", Name: "BenchmarkLRUGet"}
	sub := model.Series{Module: "systems/social-feed", Package: "github.com/x/Faultline/systems/social-feed/internal/timeline", Name: "BenchmarkGetTimeline/followees=13"}
	cmd := model.Series{Module: "systems/payment-platform", Package: "payment-platform", Name: "BenchmarkCreate"}
	for _, c := range []struct {
		s    model.Series
		want string
	}{{root, "LRUGet"}, {sub, "internal/timeline: GetTimeline/followees=13"}, {cmd, "Create"}} {
		if got := DisplayName(c.s); got != c.want {
			t.Errorf("%q, want %q", got, c.want)
		}
	}
}

func TestReportStatesTheEnvironmentAndWarnsAgainstComparingMachines(t *testing.T) {
	e := env()
	e.Dirty = true
	md := Markdown(file(e, ser("labs/a", "BenchmarkX", []float64{10, 11, 10, 10, 12}, 0, 0)))
	for _, want := range []string{"Xeon @ 2.10GHz, 1 logical CPU(s)", "linux/amd64", "go1.22.2", "abc1234 (with uncommitted changes)", "2026-11-01", "5 of each benchmark, 200ms each",
		"numbers from one machine", "Do not compare them with a table made on different hardware"} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in\n%s", want, md)
		}
	}
}

func TestReportShowsMedianAndRangeAndMarksNoisyBenchmarks(t *testing.T) {
	calm := ser("labs/a", "BenchmarkCalm", []float64{100, 101, 99, 100, 102}, 1, 16)
	wild := ser("labs/a", "BenchmarkWild", []float64{100, 160, 90, 100, 140}, 1, 16)
	md := Markdown(file(env(), calm, wild))
	if !strings.Contains(md, "| Calm | 100 ns (99.0 ns to 102 ns) | 16 | 1 | 5 |") {
		t.Errorf("a calm benchmark should show its median and range without a star:\n%s", md)
	}
	if !strings.Contains(md, "| Wild | 100 ns (90.0 ns to 160 ns) * |") {
		t.Errorf("a benchmark whose runs differ by 70%% must be starred:\n%s", md)
	}
	if !strings.Contains(md, "2 benchmarks in 1 modules, 1 of them noisy") {
		t.Errorf("summary line:\n%s", md)
	}
}

func TestReportGroupsByModuleInOrderAndListsFailures(t *testing.T) {
	f := file(env(), ser("systems/z", "BenchmarkZ", []float64{1, 1, 1}, 0, 0), ser("labs/a", "BenchmarkA", []float64{1, 1, 1}, 0, 0))
	f.Failures = []model.Failure{{Module: "labs/broken", Message: "panic: boom"}}
	md := Markdown(f)
	if strings.Index(md, "## labs/a") > strings.Index(md, "## systems/z") || strings.Index(md, "## labs/a") < 0 {
		t.Errorf("modules should be in order:\n%s", md)
	}
	if !strings.Contains(md, "## Failures") || !strings.Contains(md, "`labs/broken`: panic: boom") {
		t.Errorf("a failed module must be reported, not silently absent:\n%s", md)
	}
}

func TestBenchmarksWithoutAllocationDataShowNA(t *testing.T) {
	md := Markdown(file(env(), ser("systems/p", "BenchmarkScript", []float64{250, 250, 250}, -1, 0)))
	if !strings.Contains(md, "| n/a | n/a | 3 |") {
		t.Errorf("a script benchmark has no B/op or allocs/op:\n%s", md)
	}
}

func TestCustomMetricsGetTheirOwnColumn(t *testing.T) {
	s := ser("labs/a", "BenchmarkX", []float64{10, 10, 10}, 0, 0)
	s.Samples["ratio"] = []float64{0.5, 0.5, 0.5}
	md := Markdown(file(env(), s))
	if !strings.Contains(md, "| ratio |") || !strings.Contains(md, " 0.5 |") {
		t.Errorf("%s", md)
	}
}

// ------------------------------------------------------------------ compare

func opts() CompareOptions { return CompareOptions{Threshold: 0.05, Alpha: 0.05} }

func verdictOf(t *testing.T, old, cur []float64) Row {
	t.Helper()
	c := Compare(file(env(), ser("m", "BenchmarkX", old, 1, 16)), file(env(), ser("m", "BenchmarkX", cur, 1, 16)), opts())
	if len(c.Rows) != 1 {
		t.Fatalf("rows: %+v", c.Rows)
	}
	return c.Rows[0]
}

func TestACleanSlowdownIsCalledSlower(t *testing.T) {
	r := verdictOf(t, []float64{100, 101, 99, 100, 102}, []float64{150, 152, 149, 151, 150})
	if r.Verdict != Slower || r.Delta < 0.45 || r.Delta > 0.55 {
		t.Errorf("%+v", r)
	}
	c := Compare(file(env(), ser("m", "BenchmarkX", []float64{100, 101, 99, 100, 102}, 1, 16)), file(env(), ser("m", "BenchmarkX", []float64{150, 152, 149, 151, 150}, 1, 16)), opts())
	if !c.HasRegression() {
		t.Error("a slowdown is a regression")
	}
}

func TestACleanSpeedupIsCalledFaster(t *testing.T) {
	if r := verdictOf(t, []float64{150, 152, 149, 151, 150}, []float64{100, 101, 99, 100, 102}); r.Verdict != Faster {
		t.Errorf("%+v", r)
	}
}

func TestNoiseIsNotCalledAChange(t *testing.T) {
	if r := verdictOf(t, []float64{100, 140, 90, 125, 105, 98}, []float64{102, 95, 138, 99, 120, 110}); r.Verdict != Same {
		t.Errorf("two noisy samples from the same range must not be reported as different: %+v", r)
	}
}

func TestARealButTinyChangeIsNotWorthReporting(t *testing.T) {
	// Perfectly separated, so the p-value is as small as it gets, but only 1% apart.
	r := verdictOf(t, []float64{100.0, 100.1, 100.2, 100.3, 100.4}, []float64{101.0, 101.1, 101.2, 101.3, 101.4})
	if r.P >= 0.05 {
		t.Fatalf("setup: this should be statistically significant, p=%v", r.P)
	}
	if r.Verdict != Same {
		t.Errorf("a 1%% change is below the 5%% threshold and should not be reported: %+v", r)
	}
}

// The size of a change and the evidence for it are separate questions. This
// shifts the median by 17%, which clears any size threshold, but the runs
// overlap so much that the shift is well within the noise.
func TestABigMedianShiftInHeavilyOverlappingRunsIsNotCalledReal(t *testing.T) {
	old := []float64{90, 100, 110, 150, 160, 170}
	cur := []float64{95, 105, 140, 165, 175, 185}
	r := verdictOf(t, old, cur)
	if r.Delta < 0.10 {
		t.Fatalf("setup: the median should have moved by well over 5%%, got %.1f%%", r.Delta*100)
	}
	if r.P < 0.05 {
		t.Fatalf("setup: these runs should overlap enough to be unconvincing, p=%v", r.P)
	}
	if r.Verdict != Same {
		t.Errorf("a large but insignificant shift must not be reported as real: %+v", r)
	}
}

func TestTooFewRunsIsSaidOutrightNotGuessedAt(t *testing.T) {
	r := verdictOf(t, []float64{100, 101, 99}, []float64{200, 201, 199})
	if r.Verdict != TooFew {
		t.Errorf("with three runs each the best possible p is 0.1, so nothing can be called significant: %+v", r)
	}
}

func TestMinP(t *testing.T) {
	for _, c := range []struct {
		n1, n2 int
		want   float64
	}{{3, 3, 0.1}, {5, 5, 2.0 / 252}, {4, 4, 2.0 / 70}, {1, 1, 1}} {
		if got := minP(c.n1, c.n2); got < c.want-1e-12 || got > c.want+1e-12 {
			t.Errorf("minP(%d,%d) = %v, want %v", c.n1, c.n2, got, c.want)
		}
	}
}

func TestDifferentMachinesAreNotComparedOnTimeButStillOnAllocations(t *testing.T) {
	other := env()
	other.CPU = "Apple M2"
	old := file(env(), ser("m", "BenchmarkX", []float64{100, 101, 99, 100, 102}, 1, 16))
	cur := file(other, ser("m", "BenchmarkX", []float64{50, 51, 49, 50, 52}, 3, 48))

	c := Compare(old, cur, opts())
	r := c.Rows[0]
	if c.TimingComparable || r.Verdict != NotDone {
		t.Errorf("timings must not be compared across CPUs: %+v", r)
	}
	if !r.HasAllocs || r.NewAllocs != 3 || !c.HasRegression() {
		t.Errorf("allocation counts do not depend on the machine, so a rise is still a regression: %+v", r)
	}
	md := c.Markdown()
	if !strings.Contains(md, "Timings were not compared") || !strings.Contains(md, "Apple M2") || !strings.Contains(md, "1 to 3") {
		t.Errorf("%s", md)
	}

	forced := Compare(old, cur, CompareOptions{Threshold: 0.05, Alpha: 0.05, Force: true})
	if forced.Rows[0].Verdict != Faster || !strings.Contains(forced.Markdown(), "compared anyway because --force") {
		t.Errorf("--force compares, and says it should not be trusted: %+v", forced.Rows[0])
	}
}

func TestAddedAndRemovedBenchmarksAreListed(t *testing.T) {
	old := file(env(), ser("m", "BenchmarkKept", []float64{1, 1, 1}, 0, 0), ser("m", "BenchmarkGone", []float64{1, 1, 1}, 0, 0))
	cur := file(env(), ser("m", "BenchmarkKept", []float64{1, 1, 1}, 0, 0), ser("m", "BenchmarkNew", []float64{1, 1, 1}, 0, 0))
	c := Compare(old, cur, opts())
	if len(c.Added) != 1 || len(c.Removed) != 1 || !strings.Contains(c.Markdown(), "Only in the old results") {
		t.Errorf("%+v", c)
	}
}

func TestFewerAllocationsAreAnImprovementNotARegression(t *testing.T) {
	c := Compare(file(env(), ser("m", "BenchmarkX", []float64{100, 100, 100, 100, 100}, 3, 48)), file(env(), ser("m", "BenchmarkX", []float64{100, 100, 100, 100, 100}, 1, 16)), opts())
	if c.HasRegression() || c.AllocsDown() != 1 {
		t.Errorf("%+v", c)
	}
}

func TestTheComparisonTableReadsClearly(t *testing.T) {
	c := Compare(file(env(), ser("labs/a", "BenchmarkX", []float64{100, 101, 99, 100, 102}, 1, 16)), file(env(), ser("labs/a", "BenchmarkX", []float64{150, 152, 149, 151, 150}, 1, 16)), opts())
	md := c.Markdown()
	for _, want := range []string{"1 slower", "| labs/a: X | 100 ns | 150 ns | +50.", "| slower |", "Mann-Whitney U test"} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in\n%s", want, md)
		}
	}
}

func TestTheComparisonSaysHowManyFlagsToExpectByChance(t *testing.T) {
	var old, cur []model.Series
	for i := 0; i < 40; i++ {
		name := fmt.Sprintf("Benchmark%d", i)
		old = append(old, ser("m", name, []float64{100, 101, 99, 100, 102, 98}, 0, 0))
		cur = append(cur, ser("m", name, []float64{100, 101, 99, 100, 102, 98}, 0, 0))
	}
	c := Compare(file(env(), old...), file(env(), cur...), opts())
	if got := c.ExpectedByChance(); got != 2 {
		t.Errorf("40 benchmarks at alpha 0.05 should expect 2 by chance, got %v", got)
	}
	if md := c.Markdown(); !strings.Contains(md, "about 2 would be flagged by chance") {
		t.Errorf("the report should say so:\n%s", md)
	}
	// With only a few benchmarks there is nothing worth saying.
	few := Compare(file(env(), old[:3]...), file(env(), cur[:3]...), opts())
	if strings.Contains(few.Markdown(), "by chance") {
		t.Error("three benchmarks expect well under one false flag, so the sentence would only add noise")
	}
	// Benchmarks that could not be tested do not count towards it.
	tooFew := Compare(file(env(), ser("m", "BenchmarkX", []float64{1, 2, 3}, 0, 0)), file(env(), ser("m", "BenchmarkX", []float64{1, 2, 3}, 0, 0)), opts())
	if tooFew.ExpectedByChance() != 0 {
		t.Errorf("%v", tooFew.ExpectedByChance())
	}
}
