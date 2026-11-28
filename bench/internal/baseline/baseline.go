// Package baseline records how many allocations each benchmark makes, and
// checks later runs against it.
//
// Time is noisy: the same code on the same machine varies by tens of percent
// from run to run, so a CI job that fails on a slowdown cries wolf all the
// time and gets ignored. Allocations are different. The Go runtime counts
// them, and for the same code they come out the same every time. A change from
// zero allocations to one is a real regression that no amount of noise can
// hide or fake, which makes it something a build can safely fail on.
//
// One caveat. Go reports allocations per operation as a whole number, so an
// allocation that happens once every few thousand operations, such as a map or
// slice growing, rounds down to zero at the fixed iteration count used here.
// The baseline holds a benchmark to what it allocates per operation, not to
// what it allocates amortized over a long run.
package baseline

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/Sumit6258/Faultline/bench/internal/model"
	"github.com/Sumit6258/Faultline/bench/internal/stats"
)

const Version = 1

type Entry struct {
	Allocs float64 `json:"allocs_per_op"`
	Bytes  float64 `json:"bytes_per_op"`
}

type Baseline struct {
	Version int `json:"version"`
	// Allocation counts can differ between platforms and Go versions, because the compiler makes
	// different choices about what escapes to the heap. A baseline is only checked against a
	// matching platform, and says so when it is not.
	GOOS      string           `json:"goos"`
	GOARCH    string           `json:"goarch"`
	GoMinor   string           `json:"go_minor"`
	BenchTime string           `json:"benchtime"`
	Entries   map[string]Entry `json:"entries"`
	// Unstable benchmarks gave different allocation counts from run to run, so there is nothing
	// firm to hold them to, and they are listed here rather than quietly left out.
	Unstable []string `json:"unstable,omitempty"`
}

func (b Baseline) Save(path string) error {
	b.Version = Version
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

func Load(path string) (Baseline, error) {
	var b Baseline
	raw, err := os.ReadFile(path)
	if err != nil {
		return b, err
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return b, fmt.Errorf("%s: %w", path, err)
	}
	if b.Version != Version {
		return b, fmt.Errorf("%s is baseline format version %d, this tool reads version %d", path, b.Version, Version)
	}
	return b, nil
}

// Build records a baseline from results that hold several runs of each benchmark.
func Build(f model.File) Baseline {
	b := Baseline{
		Version: Version, GOOS: f.Env.GOOS, GOARCH: f.Env.GOARCH,
		GoMinor: model.MinorGoVersion(f.Env.GoVersion), BenchTime: f.Env.BenchTime,
		Entries: map[string]Entry{},
	}
	for _, s := range f.Series {
		allocs, ok := s.Samples["allocs/op"]
		if !ok || len(allocs) == 0 {
			continue // a benchmark that does not report allocations has nothing to record
		}
		a := stats.Summarize(allocs)
		bytes := stats.Summarize(s.Samples["B/op"])
		if a.Min != a.Max || (bytes.Max-bytes.Min) > maxF(16, 0.05*bytes.Median) {
			b.Unstable = append(b.Unstable, s.Key())
			continue
		}
		b.Entries[s.Key()] = Entry{Allocs: a.Median, Bytes: bytes.Max} // the larger byte figure: the bound to stay under
	}
	sort.Strings(b.Unstable)
	return b
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

type Options struct {
	// AllocTolerance is how many more allocations per operation are tolerated. Zero means none.
	AllocTolerance float64
	// AllocRelTolerance is a fraction of the recorded count that is also tolerated, for benchmarks that
	// allocate at least RelToleranceFloor times. Go's maps allocate a slightly different number of overflow
	// buckets from run to run, so a benchmark that fills a large map can report 880 allocations one run and 881
	// the next. Exactness is kept where it matters, on counts below the floor, and above all on zero.
	AllocRelTolerance float64
	// Bytes may grow by this factor, plus BytesSlack, before it counts. Bytes are less exact than
	// counts, because a slice that grows by doubling lands on different sizes, so this is loose.
	BytesFactor, BytesSlack float64
}

// RelToleranceFloor is the allocation count from which the relative tolerance applies.
const RelToleranceFloor = 100

func DefaultOptions() Options {
	return Options{AllocTolerance: 0, AllocRelTolerance: 0.01, BytesFactor: 1.25, BytesSlack: 64}
}

// allowed is how many allocations per operation a benchmark with this baseline may make before it counts as a regression.
func (o Options) allowed(baseline float64) float64 {
	tol := o.AllocTolerance
	if baseline >= RelToleranceFloor {
		tol = math.Max(tol, math.Floor(baseline*o.AllocRelTolerance))
	}
	return baseline + tol
}

type Finding struct {
	Key     string
	Message string
}

type Result struct {
	Checked      int
	Regressions  []Finding
	Improvements []Finding
	Missing      []string // in the baseline, absent from the run: coverage was lost
	New          []string // measured now, not in the baseline
	// Mismatch is set when the baseline was recorded on a different platform, and nothing was checked.
	Mismatch string
}

// Failed says whether a build should fail. Missing benchmarks fail it, because deleting a benchmark
// is the easiest way to make a regression disappear. A platform mismatch fails it only if asked to.
func (r Result) Failed(requireMatch, allowMissing bool) bool {
	if r.Mismatch != "" {
		return requireMatch
	}
	return len(r.Regressions) > 0 || (!allowMissing && len(r.Missing) > 0)
}

func Check(b Baseline, cur model.File, o Options) Result {
	var r Result
	if cur.Env.GOOS != b.GOOS || cur.Env.GOARCH != b.GOARCH || model.MinorGoVersion(cur.Env.GoVersion) != b.GoMinor {
		r.Mismatch = fmt.Sprintf("the baseline was recorded on %s/%s with %s, this run is %s/%s with %s",
			b.GOOS, b.GOARCH, b.GoMinor, cur.Env.GOOS, cur.Env.GOARCH, model.MinorGoVersion(cur.Env.GoVersion))
		return r
	}
	seen := map[string]bool{}
	unstable := map[string]bool{}
	for _, k := range b.Unstable {
		unstable[k] = true
	}
	for _, s := range cur.Series {
		key := s.Key()
		seen[key] = true
		e, ok := b.Entries[key]
		if !ok {
			if _, hasAllocs := s.Samples["allocs/op"]; hasAllocs && !unstable[key] {
				r.New = append(r.New, key)
			}
			continue
		}
		r.Checked++
		allocs := stats.Summarize(s.Samples["allocs/op"]).Median
		bytes := stats.Summarize(s.Samples["B/op"]).Median
		switch {
		case allocs > o.allowed(e.Allocs):
			r.Regressions = append(r.Regressions, Finding{key, fmt.Sprintf("allocs/op went from %.0f to %.0f", e.Allocs, allocs)})
		case bytes > e.Bytes*o.BytesFactor+o.BytesSlack:
			r.Regressions = append(r.Regressions, Finding{key, fmt.Sprintf("B/op went from %.0f to %.0f", e.Bytes, bytes)})
		case allocs < e.Allocs:
			r.Improvements = append(r.Improvements, Finding{key, fmt.Sprintf("allocs/op fell from %.0f to %.0f", e.Allocs, allocs)})
		}
	}
	for key := range b.Entries {
		if !seen[key] {
			r.Missing = append(r.Missing, key)
		}
	}
	sort.Strings(r.Missing)
	sort.Strings(r.New)
	sort.Slice(r.Regressions, func(i, j int) bool { return r.Regressions[i].Key < r.Regressions[j].Key })
	sort.Slice(r.Improvements, func(i, j int) bool { return r.Improvements[i].Key < r.Improvements[j].Key })
	return r
}

func (r Result) Text(b Baseline, requireMatch bool) string {
	var out strings.Builder
	if r.Mismatch != "" {
		fmt.Fprintf(&out, "Nothing was checked: %s. Allocation counts can differ between platforms, so the baseline is only enforced on the one it was recorded on.\n", r.Mismatch)
		if requireMatch {
			out.WriteString("A matching baseline was required, so this is a failure. Record one for this platform with `faultbench check -update`.\n")
		}
		return out.String()
	}
	fmt.Fprintf(&out, "Checked %d benchmarks against the allocation baseline (%s/%s, %s).\n", r.Checked, b.GOOS, b.GOARCH, b.GoMinor)
	for _, f := range r.Regressions {
		fmt.Fprintf(&out, "  REGRESSION  %s: %s\n", f.Key, f.Message)
	}
	for _, k := range r.Missing {
		fmt.Fprintf(&out, "  MISSING     %s is in the baseline but was not measured. Was it deleted, or did it fail?\n", k)
	}
	for _, f := range r.Improvements {
		fmt.Fprintf(&out, "  improved    %s: %s. Run `faultbench check -update` to lock the gain in.\n", f.Key, f.Message)
	}
	for _, k := range r.New {
		fmt.Fprintf(&out, "  new         %s has no baseline yet.\n", k)
	}
	return out.String()
}
