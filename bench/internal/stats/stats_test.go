package stats

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

func TestSummarize(t *testing.T) {
	s := Summarize([]float64{10, 12, 11, 30, 10})
	if s.N != 5 || s.Median != 11 || s.Min != 10 || s.Max != 30 {
		t.Errorf("%+v", s)
	}
	if math.Abs(s.Spread-(30-10)/11.0) > 1e-12 {
		t.Errorf("spread %v", s.Spread)
	}
	if got := Summarize([]float64{1, 2, 3, 4}).Median; got != 2.5 {
		t.Errorf("even count median %v, want 2.5", got)
	}
	if got := Summarize(nil); got.N != 0 || got.Median != 0 {
		t.Errorf("empty: %+v", got)
	}
}

func TestSummarizeDoesNotDisturbItsInput(t *testing.T) {
	in := []float64{3, 1, 2}
	Summarize(in)
	if in[0] != 3 || in[1] != 1 || in[2] != 2 {
		t.Errorf("the caller's slice was sorted: %v", in)
	}
}

func TestAMedianIsNotMovedByOneWildRun(t *testing.T) {
	calm := Summarize([]float64{100, 101, 99, 100, 102})
	wild := Summarize([]float64{100, 101, 99, 100, 5000})
	if wild.Median != calm.Median && math.Abs(wild.Median-calm.Median) > 1 {
		t.Errorf("median %v moved far from %v", wild.Median, calm.Median)
	}
	if wild.Mean < 500 {
		t.Error("the mean would have been dragged up, which is why the median is used")
	}
	if wild.Spread < 10 {
		t.Errorf("the spread should expose the wild run, got %v", wild.Spread)
	}
}

type oracleCase struct {
	A     []float64 `json:"a"`
	B     []float64 `json:"b"`
	P     float64   `json:"p"`
	Exact bool      `json:"exact"`
}

// scipy.stats.mannwhitneyu produced these p-values, from random samples that
// include clear separation, no difference, heavy ties and outliers. See
// testdata/make_oracle.py.
func TestMannWhitneyAgreesWithScipy(t *testing.T) {
	raw, err := os.ReadFile("testdata/mannwhitney_oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []oracleCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 300 {
		t.Fatalf("only %d oracle cases", len(cases))
	}
	var exact, approx int
	for i, c := range cases {
		p, gotExact := MannWhitney(c.A, c.B)
		if gotExact != c.Exact {
			t.Errorf("case %d: exact=%v, scipy was asked for exact=%v", i, gotExact, c.Exact)
			continue
		}
		tol := 1e-9
		if !c.Exact {
			tol = 1e-7
			approx++
		} else {
			exact++
		}
		if math.Abs(p-c.P) > tol {
			t.Errorf("case %d (n=%d,%d exact=%v): p=%.12g, scipy says %.12g", i, len(c.A), len(c.B), c.Exact, p, c.P)
		}
	}
	if exact < 100 || approx < 50 {
		t.Errorf("the oracle should cover both methods, got %d exact and %d approximate", exact, approx)
	}
}

func TestMannWhitneyKnownValues(t *testing.T) {
	// Fully separated samples of 5 and 5: only 2 of C(10,5)=252 arrangements are as extreme, p = 2/252.
	p, exact := MannWhitney([]float64{1, 2, 3, 4, 5}, []float64{6, 7, 8, 9, 10})
	if !exact || math.Abs(p-2.0/252) > 1e-12 {
		t.Errorf("p=%v exact=%v, want 2/252", p, exact)
	}
	// The same samples the other way round give the same two sided answer.
	if q, _ := MannWhitney([]float64{6, 7, 8, 9, 10}, []float64{1, 2, 3, 4, 5}); math.Abs(q-p) > 1e-15 {
		t.Errorf("the test is not symmetric: %v vs %v", q, p)
	}
	// Identical samples cannot be told apart.
	if p, _ := MannWhitney([]float64{5, 5, 5, 5}, []float64{5, 5, 5, 5}); p != 1 {
		t.Errorf("identical samples: p=%v, want 1", p)
	}
	if p, _ := MannWhitney(nil, []float64{1}); p != 1 {
		t.Errorf("an empty sample says nothing: p=%v", p)
	}
}

func TestTooFewRunsCanNeverBeSignificant(t *testing.T) {
	// With three runs each, even perfect separation only reaches p = 0.1. A tool must not claim
	// a significant change from that, and this is the arithmetic behind a "need more runs" message.
	p, _ := MannWhitney([]float64{1, 2, 3}, []float64{10, 11, 12})
	if math.Abs(p-0.1) > 1e-12 {
		t.Errorf("p=%v, want 0.1", p)
	}
}

func TestOverlappingNoisyRunsAreNotSignificant(t *testing.T) {
	a := []float64{100, 140, 90, 125, 105, 98}
	b := []float64{102, 95, 138, 99, 120, 110}
	if p, _ := MannWhitney(a, b); p < 0.3 {
		t.Errorf("p=%v: samples drawn from the same noisy range should not look different", p)
	}
}
