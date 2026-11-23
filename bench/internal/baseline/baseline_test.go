package baseline

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sumit6258/Faultline/bench/internal/model"
)

func env() model.Environment {
	return model.Environment{GOOS: "linux", GOARCH: "amd64", GoVersion: "go1.22.2", BenchTime: "1000x"}
}

func ser(name string, allocs, bytes []float64) model.Series {
	return model.Series{Module: "m", Package: "p", Name: name, Samples: map[string][]float64{"ns/op": make([]float64, len(allocs)), "allocs/op": allocs, "B/op": bytes}}
}

func file(e model.Environment, ss ...model.Series) model.File {
	return model.File{Version: model.Version, Env: e, Series: ss}
}

func rep(v float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestBuildRecordsStableBenchmarksAndSetsAsideUnstableOnes(t *testing.T) {
	b := Build(file(env(),
		ser("BenchmarkStable", rep(2, 3), rep(64, 3)),
		ser("BenchmarkZero", rep(0, 3), rep(0, 3)),
		ser("BenchmarkFlaky", []float64{2, 3, 2}, rep(64, 3)),
		ser("BenchmarkBytesDrift", rep(1, 3), []float64{100, 400, 100}),
		model.Series{Module: "m", Package: "p", Name: "BenchmarkScript", Samples: map[string][]float64{"ns/op": {1, 2, 3}}},
	))
	if e := b.Entries["m|p|BenchmarkStable"]; e.Allocs != 2 || e.Bytes != 64 {
		t.Errorf("stable: %+v", e)
	}
	if e, ok := b.Entries["m|p|BenchmarkZero"]; !ok || e.Allocs != 0 {
		t.Errorf("a zero allocation benchmark is the most valuable thing to hold the line on: %+v %v", e, ok)
	}
	if len(b.Unstable) != 2 || b.Unstable[0] != "m|p|BenchmarkBytesDrift" || b.Unstable[1] != "m|p|BenchmarkFlaky" {
		t.Errorf("unstable: %v", b.Unstable)
	}
	if _, ok := b.Entries["m|p|BenchmarkScript"]; ok {
		t.Error("a benchmark with no allocation data has nothing to record")
	}
	if b.GOOS != "linux" || b.GoMinor != "go1.22" || b.BenchTime != "1000x" {
		t.Errorf("%+v", b)
	}
}

func TestSaveAndLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "b.json")
	in := Build(file(env(), ser("BenchmarkA", rep(1, 3), rep(16, 3))))
	if err := in.Save(p); err != nil {
		t.Fatal(err)
	}
	out, err := Load(p)
	if err != nil || out.Entries["m|p|BenchmarkA"].Allocs != 1 || out.GoMinor != "go1.22" {
		t.Errorf("%+v %v", out, err)
	}
}

func base() Baseline {
	return Build(file(env(), ser("BenchmarkHot", rep(0, 3), rep(0, 3)), ser("BenchmarkNormal", rep(2, 3), rep(64, 3))))
}

func TestAnExtraAllocationOnAZeroAllocPathIsARegression(t *testing.T) {
	r := Check(base(), file(env(), ser("BenchmarkHot", rep(1, 1), rep(16, 1)), ser("BenchmarkNormal", rep(2, 1), rep(64, 1))), DefaultOptions())
	if len(r.Regressions) != 1 || !strings.Contains(r.Regressions[0].Message, "from 0 to 1") || !r.Failed(false, false) {
		t.Errorf("%+v", r)
	}
	if r.Checked != 2 {
		t.Errorf("checked %d", r.Checked)
	}
}

func TestMoreBytesThanTheAllowanceIsARegressionButSmallDriftIsNot(t *testing.T) {
	small := Check(base(), file(env(), ser("BenchmarkHot", rep(0, 1), rep(0, 1)), ser("BenchmarkNormal", rep(2, 1), rep(90, 1))), DefaultOptions())
	if len(small.Regressions) != 0 {
		t.Errorf("64 to 90 bytes is within the allowance: %+v", small.Regressions)
	}
	big := Check(base(), file(env(), ser("BenchmarkHot", rep(0, 1), rep(0, 1)), ser("BenchmarkNormal", rep(2, 1), rep(4096, 1))), DefaultOptions())
	if len(big.Regressions) != 1 || !strings.Contains(big.Regressions[0].Message, "B/op") {
		t.Errorf("a large buffer allocated per operation must be caught even if the count is unchanged: %+v", big)
	}
}

func TestFewerAllocationsIsAnImprovementWorthLockingIn(t *testing.T) {
	r := Check(base(), file(env(), ser("BenchmarkHot", rep(0, 1), rep(0, 1)), ser("BenchmarkNormal", rep(1, 1), rep(32, 1))), DefaultOptions())
	if len(r.Improvements) != 1 || r.Failed(false, false) {
		t.Errorf("%+v", r)
	}
}

func TestADeletedBenchmarkFailsTheCheckUnlessAllowed(t *testing.T) {
	r := Check(base(), file(env(), ser("BenchmarkNormal", rep(2, 1), rep(64, 1))), DefaultOptions())
	if len(r.Missing) != 1 || !r.Failed(false, false) {
		t.Errorf("deleting a benchmark must not be a way to make a regression disappear: %+v", r)
	}
	if r.Failed(false, true) {
		t.Error("--allow-missing should let a deliberate removal through")
	}
}

func TestNewBenchmarksAreNotedButDoNotFail(t *testing.T) {
	r := Check(base(), file(env(), ser("BenchmarkHot", rep(0, 1), rep(0, 1)), ser("BenchmarkNormal", rep(2, 1), rep(64, 1)), ser("BenchmarkBrandNew", rep(5, 1), rep(80, 1))), DefaultOptions())
	if len(r.New) != 1 || r.Failed(false, false) {
		t.Errorf("%+v", r)
	}
}

func TestADifferentPlatformChecksNothingUnlessAMatchIsRequired(t *testing.T) {
	other := env()
	other.GOARCH = "arm64"
	cur := file(other, ser("BenchmarkHot", rep(9, 1), rep(999, 1)))
	r := Check(base(), cur, DefaultOptions())
	if r.Mismatch == "" || r.Checked != 0 || len(r.Regressions) != 0 {
		t.Errorf("allocation counts differ between platforms, so a mismatch must not produce false regressions: %+v", r)
	}
	if r.Failed(false, false) {
		t.Error("on a platform with no baseline the check should pass, quietly telling you why")
	}
	if !r.Failed(true, false) {
		t.Error("CI, which must have a baseline, can insist on one")
	}
	if !strings.Contains(r.Text(base(), true), "arm64") {
		t.Errorf("%s", r.Text(base(), true))
	}
}

func TestAPatchReleaseOfGoStillMatches(t *testing.T) {
	cur := env()
	cur.GoVersion = "go1.22.9"
	if r := Check(base(), file(cur, ser("BenchmarkHot", rep(0, 1), rep(0, 1))), DefaultOptions()); r.Mismatch != "" {
		t.Errorf("%s", r.Mismatch)
	}
}

func TestToleranceCanBeRaised(t *testing.T) {
	o := DefaultOptions()
	o.AllocTolerance = 1
	r := Check(base(), file(env(), ser("BenchmarkHot", rep(1, 1), rep(16, 1)), ser("BenchmarkNormal", rep(2, 1), rep(64, 1))), o)
	if len(r.Regressions) != 0 {
		t.Errorf("%+v", r)
	}
}
