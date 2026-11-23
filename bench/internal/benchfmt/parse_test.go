package benchfmt

import (
	"strings"
	"testing"
)

const sample = `goos: linux
goarch: amd64
pkg: example.com/a
cpu: Intel(R) Xeon(R) Processor @ 2.10GHz
BenchmarkLRUGet-8   	14293632	        14.53 ns/op	       0 B/op	       0 allocs/op
BenchmarkLRUSet-8   	 7000000	        82.80 ns/op	      16 B/op	       1 allocs/op
BenchmarkScrape/series=100-8         	    5026	     49242 ns/op	   22972 B/op	     328 allocs/op
some log line from the benchmark
    --- BENCH: BenchmarkLRUGet-8
        bench_test.go:12: a b.Log line
PASS
ok  	example.com/a	3.1s
pkg: example.com/b
BenchmarkQueue   	 9000000	       138.1 ns/op	      48 B/op	       1 allocs/op	         0.5 ratio
PASS
`

func TestParsesNamesProcsMetricsAndHeaders(t *testing.T) {
	out, err := Parse(strings.NewReader(sample), "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if out.GOOS != "linux" || out.GOARCH != "amd64" || out.CPU != "Intel(R) Xeon(R) Processor @ 2.10GHz" {
		t.Errorf("header: %+v", out)
	}
	if len(out.Samples) != 4 {
		t.Fatalf("got %d samples, want 4: %+v", len(out.Samples), out.Samples)
	}
	get := out.Samples[0]
	if get.Name != "BenchmarkLRUGet" || get.Procs != 8 || get.Package != "example.com/a" || get.Iters != 14293632 {
		t.Errorf("first sample: %+v", get)
	}
	if get.Metrics["ns/op"] != 14.53 || get.Metrics["allocs/op"] != 0 {
		t.Errorf("metrics: %v", get.Metrics)
	}
}

func TestSubBenchmarkNamesKeepTheirSlashAndEquals(t *testing.T) {
	out, _ := Parse(strings.NewReader(sample), "")
	if out.Samples[2].Name != "BenchmarkScrape/series=100" || out.Samples[2].Metrics["B/op"] != 22972 {
		t.Errorf("%+v", out.Samples[2])
	}
}

func TestCustomMetricsAreKeptAndThePackageFollowsThePkgLine(t *testing.T) {
	out, _ := Parse(strings.NewReader(sample), "")
	q := out.Samples[3]
	if q.Package != "example.com/b" || q.Metrics["ratio"] != 0.5 {
		t.Errorf("%+v", q)
	}
}

func TestNoSuffixMeansOneProc(t *testing.T) {
	out, _ := Parse(strings.NewReader(sample), "")
	if q := out.Samples[3]; q.Procs != 1 || q.Name != "BenchmarkQueue" {
		t.Errorf("GOMAXPROCS=1 prints no -N suffix, got %+v", q)
	}
}

func TestANameThatEndsInDigitsAfterADashIsOnlyTrimmedOnce(t *testing.T) {
	out, _ := Parse(strings.NewReader("BenchmarkX/size-100-8  1000  5.0 ns/op\n"), "p")
	if s := out.Samples[0]; s.Name != "BenchmarkX/size-100" || s.Procs != 8 {
		t.Errorf("%+v", s)
	}
}

func TestLinesThatAreNotMeasurementsAreIgnored(t *testing.T) {
	junk := strings.Join([]string{
		"BenchmarkLooksLikeOne but is prose",
		"BenchmarkNoIters  abc  5 ns/op",
		"BenchmarkOddFields  100  5 ns/op  7",
		"BenchmarkNoTime  100  5 B/op  1 allocs/op",
		"BenchmarkZeroIters  0  5 ns/op",
		"BenchmarkGood  100  5 ns/op",
	}, "\n")
	out, _ := Parse(strings.NewReader(junk), "p")
	if len(out.Samples) != 1 || out.Samples[0].Name != "BenchmarkGood" {
		t.Errorf("only the well formed line is a measurement: %+v", out.Samples)
	}
}

func TestFailuresAndPanicsAreReported(t *testing.T) {
	for _, text := range []string{"--- FAIL: BenchmarkX (0.00s)\n", "FAIL\texample.com/a\t0.1s\n", "panic: boom\n"} {
		out, _ := Parse(strings.NewReader(text), "")
		if !out.Failed || out.FailReason == "" {
			t.Errorf("%q should be a failure", text)
		}
	}
	if out, _ := Parse(strings.NewReader(sample), ""); out.Failed {
		t.Error("a clean run is not a failure")
	}
}

func TestOutputFromAScriptUsesTheDefaultPackage(t *testing.T) {
	out, _ := Parse(strings.NewReader("BenchmarkCreate  2000  378000 ns/op\n"), "payment-platform")
	if out.Samples[0].Package != "payment-platform" {
		t.Errorf("%+v", out.Samples[0])
	}
}
