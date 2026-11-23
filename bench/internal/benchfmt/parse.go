// Package benchfmt reads the text that `go test -bench` prints.
//
// The format is informal, so this is deliberately forgiving about what it
// ignores (log lines, PASS, ok) and strict about what it accepts as a
// measurement: a name, an iteration count, then pairs of a number and a unit.
package benchfmt

import (
	"bufio"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Sample is one line of benchmark output: one run of one benchmark.
type Sample struct {
	Package string
	Name    string // without the trailing -N that Go appends for GOMAXPROCS
	Procs   int    // the N in that suffix, or 1 when Go printed none
	Iters   int64
	Metrics map[string]float64 // "ns/op", "B/op", "allocs/op", and anything reported with b.ReportMetric
}

// Output is everything learned from one invocation.
type Output struct {
	Samples []Sample
	// Machine facts, from the header lines Go prints before each package's benchmarks.
	GOOS, GOARCH, CPU string
	// Failed is set if the output shows a failing test, a failing benchmark or a panic.
	Failed     bool
	FailReason string
}

var procsSuffix = regexp.MustCompile(`-(\d+)$`)

// Parse reads output. defaultPkg names samples that appear with no pkg: line
// before them, which is how a benchmark run from a plain script looks.
func Parse(r io.Reader, defaultPkg string) (Output, error) {
	var out Output
	pkg := defaultPkg
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "pkg: "):
			pkg = strings.TrimSpace(strings.TrimPrefix(line, "pkg: "))
		case strings.HasPrefix(line, "goos: "):
			out.GOOS = strings.TrimSpace(strings.TrimPrefix(line, "goos: "))
		case strings.HasPrefix(line, "goarch: "):
			out.GOARCH = strings.TrimSpace(strings.TrimPrefix(line, "goarch: "))
		case strings.HasPrefix(line, "cpu: "):
			out.CPU = strings.TrimSpace(strings.TrimPrefix(line, "cpu: "))
		case strings.HasPrefix(line, "--- FAIL"), strings.HasPrefix(line, "FAIL"), strings.HasPrefix(line, "panic:"):
			if !out.Failed {
				out.Failed, out.FailReason = true, strings.TrimSpace(line)
			}
		case strings.HasPrefix(line, "Benchmark"):
			if s, ok := parseLine(line); ok {
				s.Package = pkg
				out.Samples = append(out.Samples, s)
			}
		}
	}
	return out, sc.Err()
}

func parseLine(line string) (Sample, bool) {
	f := strings.Fields(line)
	// name, iterations, then at least one value and its unit
	if len(f) < 4 || (len(f)-2)%2 != 0 {
		return Sample{}, false
	}
	iters, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil || iters <= 0 {
		return Sample{}, false
	}
	s := Sample{Name: f[0], Procs: 1, Iters: iters, Metrics: map[string]float64{}}
	if m := procsSuffix.FindStringSubmatch(s.Name); m != nil {
		s.Procs, _ = strconv.Atoi(m[1])
		s.Name = strings.TrimSuffix(s.Name, m[0])
	}
	for i := 2; i < len(f); i += 2 {
		v, err := strconv.ParseFloat(f[i], 64)
		if err != nil {
			return Sample{}, false
		}
		s.Metrics[f[i+1]] = v
	}
	if _, ok := s.Metrics["ns/op"]; !ok {
		return Sample{}, false // not a measurement a benchmark would print
	}
	return s, true
}
