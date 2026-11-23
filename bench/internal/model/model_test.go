package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env() Environment {
	return Environment{GOOS: "linux", GOARCH: "amd64", CPU: "Xeon @ 2.10GHz", GoVersion: "go1.22.2", NumCPU: 1, Count: 5, BenchTime: "200ms", Date: "2026-11-01T00:00:00Z"}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.json")
	in := File{Env: env(), Series: []Series{{Module: "labs/x", Package: "p", Name: "BenchmarkA", Procs: 1, Samples: map[string][]float64{"ns/op": {1, 2, 3}, "allocs/op": {0, 0, 0}}}}}
	if err := in.Save(path); err != nil {
		t.Fatal(err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if out.Version != Version || out.Env != in.Env || len(out.Series) != 1 || out.Series[0].Samples["ns/op"][2] != 3 {
		t.Errorf("round trip changed the data: %+v", out)
	}
}

func TestAFileFromAnotherVersionIsRefusedNotMisread(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.json")
	os.WriteFile(path, []byte(`{"version": 99, "series": []}`), 0o644)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "version 99") {
		t.Errorf("err = %v", err)
	}
	os.WriteFile(path, []byte(`not json`), 0o644)
	if _, err := Load(path); err == nil {
		t.Error("garbage should not load")
	}
}

func TestSeriesKeyIdentifiesAModulePackageAndName(t *testing.T) {
	a := Series{Module: "m", Package: "p", Name: "BenchmarkX"}
	b := Series{Module: "m", Package: "p2", Name: "BenchmarkX"}
	if a.Key() == b.Key() {
		t.Error("the same benchmark name in two packages is two benchmarks")
	}
}

func TestMinorGoVersion(t *testing.T) {
	for in, want := range map[string]string{"go1.22.2": "go1.22", "go1.23": "go1.23", "devel": "devel"} {
		if got := MinorGoVersion(in); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
}

func TestResultsFromDifferentMachinesAreNotComparable(t *testing.T) {
	a := env()
	if ok, why := Comparable(a, a); !ok || why != "" {
		t.Errorf("identical environments: %v %q", ok, why)
	}
	patch := a
	patch.GoVersion = "go1.22.9"
	if ok, _ := Comparable(a, patch); !ok {
		t.Error("a patch release of Go should not block a comparison")
	}
	for name, mutate := range map[string]func(*Environment){
		"another CPU":      func(e *Environment) { e.CPU = "Apple M2" },
		"another OS":       func(e *Environment) { e.GOOS = "darwin" },
		"another arch":     func(e *Environment) { e.GOARCH = "arm64" },
		"another Go minor": func(e *Environment) { e.GoVersion = "go1.23.1" },
	} {
		b := a
		mutate(&b)
		if ok, why := Comparable(a, b); ok || why == "" {
			t.Errorf("%s: comparable=%v why=%q, want a refusal with a reason", name, ok, why)
		}
	}
}
