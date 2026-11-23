package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const benchSrc = `package good

import "testing"

var sink []byte
var sink2 []byte

func BenchmarkAlloc(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sink = make([]byte, 64)
%s	}
}

func BenchmarkNoAlloc(b *testing.B) {
	b.ReportAllocs()
	x := 0
	for i := 0; i < b.N; i++ {
		x += i
	}
	_ = x
}
`

func repo(t *testing.T) string {
	root := t.TempDir()
	must := func(rel, content string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("good/go.mod", "module example.com/good\n\ngo 1.22\n")
	setAllocs(t, root, 1)
	must("bench/benchmarks.json", `{"exclude_package_suffixes": ["/internal/obs"]}`)
	return root
}

// setAllocs rewrites the benchmark so it allocates once or twice per operation.
func setAllocs(t *testing.T, root string, n int) {
	extra := ""
	if n == 2 {
		extra = "\t\tsink2 = make([]byte, 64)\n"
	}
	src := strings.Replace(benchSrc, "%s", extra, 1)
	if err := os.WriteFile(filepath.Join(root, "good", "good_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func cli(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestRunReportCompareEndToEnd(t *testing.T) {
	root := repo(t)
	res := filepath.Join(t.TempDir(), "r.json")

	code, out, errs := cli("run", "-root", root, "-count", "3", "-benchtime", "20x", "-o", res)
	if code != 0 || !strings.Contains(out, "2 benchmarks measured 3 times each") {
		t.Fatalf("run: code %d\n%s\n%s", code, out, errs)
	}
	if _, err := os.Stat(res); err != nil {
		t.Fatal("no results file")
	}

	code, md, _ := cli("report", "-in", res)
	if code != 0 || !strings.Contains(md, "| Alloc |") || !strings.Contains(md, "| NoAlloc |") {
		t.Errorf("report: code %d\n%s", code, md)
	}
	mdPath := filepath.Join(t.TempDir(), "BENCHMARKS.md")
	if code, out, _ := cli("report", "-in", res, "-o", mdPath); code != 0 || !strings.Contains(out, "wrote") {
		t.Errorf("report -o: %d %s", code, out)
	}
	if b, _ := os.ReadFile(mdPath); !strings.Contains(string(b), "# Benchmarks") {
		t.Error("report file not written")
	}

	// A file compared with itself has no regression.
	code, cmpOut, _ := cli("compare", "-fail-on-regression", res, res)
	if code != 0 || !strings.Contains(cmpOut, "0 slower") {
		t.Errorf("compare with itself: code %d\n%s", code, cmpOut)
	}

	// Make a copy in which one benchmark is twice as slow, and compare. With three runs a clean
	// difference cannot reach significance, which the tool must say instead of claiming a regression.
	var f map[string]any
	raw, _ := os.ReadFile(res)
	json.Unmarshal(raw, &f)
	for _, s := range f["series"].([]any) {
		series := s.(map[string]any)
		if series["name"] == "BenchmarkAlloc" {
			ns := series["samples"].(map[string]any)["ns/op"].([]any)
			for i := range ns {
				ns[i] = ns[i].(float64)*2 + float64(i)*0.01
			}
		}
	}
	slow := filepath.Join(t.TempDir(), "slow.json")
	b, _ := json.Marshal(f)
	os.WriteFile(slow, b, 0o644)
	code, _, _ = cli("compare", "-fail-on-regression", res, slow)
	if code != 0 {
		t.Errorf("three runs cannot prove a slowdown, so the tool must not claim one: exit %d", code)
	}
}

func TestCheckRecordsABaselineThenCatchesAnExtraAllocation(t *testing.T) {
	root := repo(t)
	bl := filepath.Join(root, "bench", "allocs-baseline.json")

	if code, out, _ := cli("check", "-root", root, "-baseline", bl); code != 0 || !strings.Contains(out, "No baseline") {
		t.Errorf("with no baseline the check should pass and say so: %d\n%s", code, out)
	}
	if code, _, _ := cli("check", "-root", root, "-baseline", bl, "-require-baseline"); code != 3 {
		t.Errorf("CI can insist on a baseline: exit %d, want 3", code)
	}

	code, out, errs := cli("check", "-root", root, "-baseline", bl, "-update")
	if code != 0 || !strings.Contains(out, "Recorded 2 benchmarks") {
		t.Fatalf("update: %d\n%s\n%s", code, out, errs)
	}

	if code, out, _ := cli("check", "-root", root, "-baseline", bl); code != 0 || !strings.Contains(out, "ok") || !strings.Contains(out, "Checked 2") {
		t.Errorf("unchanged code must pass: %d\n%s", code, out)
	}

	setAllocs(t, root, 2) // the benchmark now allocates twice per operation
	code, out, _ = cli("check", "-root", root, "-baseline", bl)
	if code != 1 || !strings.Contains(out, "REGRESSION") || !strings.Contains(out, "allocs/op went from 1 to 2") || !strings.Contains(out, "FAILED") {
		t.Errorf("an extra allocation must fail the check: %d\n%s", code, out)
	}

	if err := os.Remove(filepath.Join(root, "good", "good_test.go")); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "good", "good_test.go"), []byte("package good\n"), 0o644) // every benchmark deleted
	code, out, _ = cli("check", "-root", root, "-baseline", bl)
	if code != 1 || !strings.Contains(out, "MISSING") {
		t.Errorf("deleting the benchmarks must not make the check pass: %d\n%s", code, out)
	}
	if code, _, _ := cli("check", "-root", root, "-baseline", bl, "-allow-missing"); code != 0 {
		t.Errorf("-allow-missing is for a deliberate removal: exit %d", code)
	}
}

func TestAPartialRunDoesNotWriteABaseline(t *testing.T) {
	root := repo(t)
	os.WriteFile(filepath.Join(root, "good", "good_test.go"), []byte("package good\n\nimport \"testing\"\n\nfunc BenchmarkBoom(b *testing.B) { panic(\"no\") }\n"), 0o644)
	bl := filepath.Join(root, "bench", "allocs-baseline.json")
	code, out, _ := cli("check", "-root", root, "-baseline", bl, "-update")
	if code != 1 || !strings.Contains(out, "none was written") {
		t.Errorf("%d\n%s", code, out)
	}
	if _, err := os.Stat(bl); err == nil {
		t.Error("a baseline written from a failed run would silently drop benchmarks")
	}
}

func TestUsageErrors(t *testing.T) {
	if code, _, errs := cli(); code != 2 || !strings.Contains(errs, "usage") {
		t.Errorf("no arguments: %d %s", code, errs)
	}
	if code, _, errs := cli("frobnicate"); code != 2 || !strings.Contains(errs, "unknown command") {
		t.Errorf("unknown command: %d %s", code, errs)
	}
	if code, _, _ := cli("compare", "only-one.json"); code != 2 {
		t.Errorf("compare needs two files: %d", code)
	}
	if code, _, _ := cli("report", "-in", filepath.Join(t.TempDir(), "missing.json")); code != 2 {
		t.Errorf("a missing results file is an error: %d", code)
	}
	if code, out, _ := cli("help"); code != 0 || !strings.Contains(out, "faultbench check") {
		t.Errorf("help: %d", code)
	}
}

const workSrc = `package good

import "testing"

var sink int

func BenchmarkWork(b *testing.B) {
	for i := 0; i < b.N; i++ {
		x := 0
		for j := 0; j < %d; j++ {
			x += j * j
		}
		sink = x
	}
}
`

func workTree(t *testing.T, work int) string {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/top\n\ngo 1.22\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "good"), 0o755)
	os.WriteFile(filepath.Join(root, "good", "go.mod"), []byte("module example.com/good\n\ngo 1.22\n"), 0o644)
	os.WriteFile(filepath.Join(root, "good", "good_test.go"), []byte(strings.Replace(workSrc, "%d", strconv.Itoa(work), 1)), 0o644)
	return root
}

func TestABCommandFailsOnARegressionAndPassesOnIdenticalTrees(t *testing.T) {
	base, slow := workTree(t, 2000), workTree(t, 10000)
	save := filepath.Join(t.TempDir(), "run")

	code, out, errs := cli("ab", "-old", base, "-new", slow, "-count", "6", "-benchtime", "30ms", "-threshold", "50", "-fail-on-regression", "-o", save)
	if code != 1 || !strings.Contains(out, "1 slower") {
		t.Errorf("a tree doing five times the work must fail: %d\n%s\n%s", code, out, errs)
	}
	for _, suffix := range []string{"-old.json", "-new.json"} {
		if _, err := os.Stat(save + suffix); err != nil {
			t.Errorf("raw results not saved: %v", err)
		}
	}

	code, out, errs = cli("ab", "-old", base, "-new", base, "-count", "6", "-benchtime", "30ms", "-threshold", "50", "-fail-on-regression")
	if code != 0 || strings.Contains(out, "1 slower") {
		t.Errorf("identical trees must pass: %d\n%s\n%s", code, out, errs)
	}
}

func TestABCommandNeedsAnOldTree(t *testing.T) {
	if code, _, errs := cli("ab"); code != 2 || !strings.Contains(errs, "-old") {
		t.Errorf("%d %s", code, errs)
	}
}
