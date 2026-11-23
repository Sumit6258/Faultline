package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sumit6258/Faultline/bench/internal/report"
)

const workBench = `package good

import "testing"

var sink int

func BenchmarkWork(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		x := 0
		for j := 0; j < %d; j++ {
			x += j * j
		}
		sink = x
	}
}
`

func tree(t *testing.T, work int) string {
	root := t.TempDir()
	write(t, root, "good/go.mod", "module example.com/good\n\ngo 1.22\n")
	write(t, root, "good/good_test.go", fmt.Sprintf(workBench, work))
	return root
}

func compare(t *testing.T, res ABResult, threshold float64) report.Comparison {
	return report.Compare(res.Old, res.New, report.CompareOptions{Threshold: threshold, Alpha: 0.05})
}

func rowFor(t *testing.T, c report.Comparison, name string) report.Row {
	t.Helper()
	for _, r := range c.Rows {
		if strings.HasSuffix(r.Name, name) {
			return r
		}
	}
	t.Fatalf("no row for %s in %+v", name, c.Rows)
	return report.Row{}
}

func TestABFindsANewTreeThatDoesFiveTimesTheWork(t *testing.T) {
	res, err := RunAB(ABOptions{OldRoot: tree(t, 2000), NewRoot: tree(t, 10000), Count: 6, BenchTime: "30ms"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Old.Failures)+len(res.New.Failures) != 0 {
		t.Fatalf("failures: %+v %+v", res.Old.Failures, res.New.Failures)
	}
	r := rowFor(t, compare(t, res, 0.5), "Work")
	if r.Verdict != report.Slower || r.Delta < 2 {
		t.Errorf("a tree doing 5x the work must be called slower, got %+v", r)
	}
	if len(res.Old.Series) != 1 || len(res.Old.Series[0].Samples["ns/op"]) != 6 || len(res.New.Series[0].Samples["ns/op"]) != 6 {
		t.Errorf("each side should have one sample per round: %+v", res.Old.Series)
	}
}

func TestABOnTheSameTreeOnBothSidesFindsNoChange(t *testing.T) {
	same := tree(t, 4000)
	res, err := RunAB(ABOptions{OldRoot: same, NewRoot: same, Count: 6, BenchTime: "30ms"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Old.Series) != 1 || len(res.New.Series) != 1 {
		t.Fatalf("both sides are the same directory and must both be measured: old %d new %d series", len(res.Old.Series), len(res.New.Series))
	}
	if r := rowFor(t, compare(t, res, 0.5), "Work"); r.Verdict == report.Slower || r.Verdict == report.Faster {
		t.Errorf("identical code must not differ by 50%% with p < 0.05: %+v", r)
	}
}

func TestABAlternatesWhoGoesFirst(t *testing.T) {
	var order []string
	_, err := RunAB(ABOptions{OldRoot: tree(t, 1000), NewRoot: tree(t, 1000), Count: 4, BenchTime: "10x",
		Trace: func(side string, round int) { order = append(order, fmt.Sprintf("%s%d", side, round)) }})
	if err != nil {
		t.Fatal(err)
	}
	want := "old0 new0 new1 old1 old2 new2 new3 old3"
	if got := strings.Join(order, " "); got != want {
		t.Errorf("order %q, want %q: strictly alternating who goes first is what cancels a steady drift", got, want)
	}
}

func TestABSkipsAModuleThatIsMissingFromTheOldTree(t *testing.T) {
	oldRoot, newRoot := tree(t, 1000), tree(t, 1000)
	write(t, newRoot, "extra/go.mod", "module example.com/extra\n\ngo 1.22\n")
	write(t, newRoot, "extra/extra_test.go", "package extra\n\nimport \"testing\"\n\nfunc BenchmarkNew(b *testing.B) {\n\tfor i := 0; i < b.N; i++ {\n\t}\n}\n")
	res, err := RunAB(ABOptions{OldRoot: oldRoot, NewRoot: newRoot, Count: 2, BenchTime: "10x"})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range append(res.Old.Series, res.New.Series...) {
		if s.Module == "extra" {
			t.Errorf("a module that exists on only one side cannot be compared: %+v", s)
		}
	}
	if len(res.Old.Failures)+len(res.New.Failures) != 0 {
		t.Errorf("a missing module is not a failure: %+v %+v", res.Old.Failures, res.New.Failures)
	}
}

func TestABReportsACompileErrorAndStillComparesTheRest(t *testing.T) {
	oldRoot, newRoot := tree(t, 1000), tree(t, 1000)
	for _, root := range []string{oldRoot, newRoot} {
		write(t, root, "ok/go.mod", "module example.com/ok\n\ngo 1.22\n")
		write(t, root, "ok/ok_test.go", "package ok\n\nimport \"testing\"\n\nfunc BenchmarkOK(b *testing.B) {\n\tfor i := 0; i < b.N; i++ {\n\t}\n}\n")
	}
	write(t, newRoot, "good/good_test.go", "package good\n\nthis does not compile\n")
	res, err := RunAB(ABOptions{OldRoot: oldRoot, NewRoot: newRoot, Count: 2, BenchTime: "10x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.New.Failures) != 1 || res.New.Failures[0].Module != "good" {
		t.Errorf("the broken tree's failure should be reported: %+v", res.New.Failures)
	}
	found := false
	for _, s := range res.New.Series {
		found = found || s.Module == "ok"
	}
	if !found {
		t.Error("one module failing to build must not stop the others being measured")
	}
}

func TestABRunsScriptsInBothTreesAlternately(t *testing.T) {
	oldRoot, newRoot := t.TempDir(), t.TempDir()
	write(t, oldRoot, "s/value.txt", "BenchmarkScript 100 250 ns/op\n")
	write(t, newRoot, "s/value.txt", "BenchmarkScript 100 500 ns/op\n")
	cfg := Config{Commands: []Command{{Name: "script", Dir: "s", Argv: []string{"cat", "value.txt"}}}}
	var order []string
	res, err := RunAB(ABOptions{OldRoot: oldRoot, NewRoot: newRoot, Config: cfg, Count: 3,
		Trace: func(side string, round int) { order = append(order, side) }})
	if err != nil {
		t.Fatal(err)
	}
	o, n := res.Old.Series[0].Samples["ns/op"], res.New.Series[0].Samples["ns/op"]
	if len(o) != 3 || len(n) != 3 || o[0] != 250 || n[0] != 500 {
		t.Errorf("old %v new %v", o, n)
	}
	if strings.Join(order, ",") != "old,new,new,old,old,new" {
		t.Errorf("scripts should alternate too: %v", order)
	}
}

func TestABCountMustBePositive(t *testing.T) {
	if _, err := RunAB(ABOptions{OldRoot: t.TempDir(), NewRoot: t.TempDir(), Count: 0}); err == nil {
		t.Error("a count of zero should be refused")
	}
}

// Run from a repository root, the roots are "." and a sibling, not absolute paths.
func TestABAcceptsRelativeRootsAndModulesWithSeveralPackages(t *testing.T) {
	parent := t.TempDir()
	for _, side := range []string{"old", "new"} {
		root := filepath.Join(parent, side)
		write(t, root, "good/go.mod", "module example.com/good\n\ngo 1.22\n")
		write(t, root, "good/good_test.go", fmt.Sprintf(workBench, 2000))
		// a second package in the same module that has no benchmarks, like cmd/demo in the labs
		write(t, root, "good/cmd/demo/main.go", "package main\n\nfunc main() {}\n")
	}
	here, _ := os.Getwd()
	if err := os.Chdir(parent); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(here)

	res, err := RunAB(ABOptions{OldRoot: "old", NewRoot: "new", Count: 2, BenchTime: "10x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Old.Series) != 1 || len(res.New.Series) != 1 {
		t.Errorf("relative roots found %d and %d series, want 1 each: a module with an extra package must still pair its benchmark package", len(res.Old.Series), len(res.New.Series))
	}
	same, _ := RunAB(ABOptions{OldRoot: "new", NewRoot: "new", Count: 2, BenchTime: "10x"})
	if len(same.Old.Series) != 1 || len(same.New.Series) != 1 {
		t.Errorf("the same relative root on both sides: %d and %d series", len(same.Old.Series), len(same.New.Series))
	}
}
