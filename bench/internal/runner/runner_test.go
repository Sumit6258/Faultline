package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const goodBench = `package good

import "testing"

var sink []byte

func BenchmarkAlloc(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sink = make([]byte, 64)
	}
}

func BenchmarkSub(b *testing.B) {
	for _, n := range []int{1, 2} {
		b.Run("n="+string(rune('0'+n)), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				sink = make([]byte, n)
			}
		})
	}
}
`

func fixture(t *testing.T) string {
	root := t.TempDir()
	write(t, root, "good/go.mod", "module example.com/good\n\ngo 1.22\n")
	write(t, root, "good/good_test.go", goodBench)
	write(t, root, "good/internal/obs/obs_test.go", "package obs\n\nimport \"testing\"\n\nfunc BenchmarkVendoredCopy(b *testing.B) {\n\tfor i := 0; i < b.N; i++ {\n\t}\n}\n")
	write(t, root, "broken/go.mod", "module example.com/broken\n\ngo 1.22\n")
	write(t, root, "broken/broken_test.go", "package broken\n\nimport \"testing\"\n\nfunc BenchmarkBoom(b *testing.B) {\n\tpanic(\"it blew up\")\n}\n")
	write(t, root, "node_modules/pkg/go.mod", "module example.com/ignored\n")
	write(t, root, ".git/go.mod", "module example.com/ignored2\n")
	return root
}

func TestFindModulesSkipsDependencyAndGitDirectories(t *testing.T) {
	mods, err := FindModules(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(mods, ",") != "broken,good" {
		t.Errorf("modules: %v", mods)
	}
}

func TestRunCollectsEveryRepetitionOfEveryBenchmark(t *testing.T) {
	root := fixture(t)
	cfg := Config{
		ExcludePackageSuffixes: []string{"/internal/obs"},
		Commands:               []Command{{Name: "script", Dir: ".", Argv: []string{"sh", "-c", "printf 'BenchmarkScript 100 250 ns/op\\n'"}}},
	}
	f, err := Run(Options{Root: root, Config: cfg, Count: 3, BenchTime: "20x"})
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string][]float64{}
	for _, s := range f.Series {
		byName[s.Name] = s.Samples["ns/op"]
		if s.Name == "BenchmarkAlloc" {
			if got := s.Samples["allocs/op"]; len(got) != 3 || got[0] != 1 {
				t.Errorf("allocs/op samples %v, want three runs of 1", got)
			}
			if got := s.Samples["B/op"]; len(got) != 3 || got[0] != 64 {
				t.Errorf("B/op samples %v", got)
			}
		}
	}
	for _, name := range []string{"BenchmarkAlloc", "BenchmarkSub/n=1", "BenchmarkSub/n=2", "BenchmarkScript"} {
		if len(byName[name]) != 3 {
			t.Errorf("%s has %d samples, want 3 (one per repetition)", name, len(byName[name]))
		}
	}
	if _, ok := byName["BenchmarkVendoredCopy"]; ok {
		t.Error("a package under internal/obs is a vendored copy and must be skipped")
	}

	if len(f.Failures) != 1 || f.Failures[0].Module != "broken" || !strings.Contains(f.Failures[0].Message, "panic") {
		t.Fatalf("a panicking benchmark should be reported with what went wrong: %+v", f.Failures)
	}
	if f.Env.Count != 3 || f.Env.BenchTime != "20x" || !strings.HasPrefix(f.Env.GoVersion, "go") || f.Env.CPU == "" || f.Env.GOOS == "" || f.Env.Date == "" {
		t.Errorf("environment not captured: %+v", f.Env)
	}
}

func TestOneBrokenModuleDoesNotStopTheOthers(t *testing.T) {
	f, err := Run(Options{Root: fixture(t), Count: 1, BenchTime: "10x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Failures) != 1 || len(f.Series) == 0 {
		t.Errorf("a failing module should be reported while the rest still run: failures=%v series=%d", f.Failures, len(f.Series))
	}
}

func TestOnlyLimitsWhatRuns(t *testing.T) {
	f, err := Run(Options{Root: fixture(t), Count: 1, BenchTime: "10x", Only: []string{"good"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Failures) != 0 {
		t.Errorf("the broken module should not have been run: %+v", f.Failures)
	}
	for _, s := range f.Series {
		if s.Module != "good" {
			t.Errorf("series from %s", s.Module)
		}
	}
}

func TestAScriptThatFailsIsAFailureNotACrash(t *testing.T) {
	cfg := Config{Commands: []Command{{Name: "bad", Dir: ".", Argv: []string{"sh", "-c", "echo oops >&2; exit 3"}}}}
	f, err := Run(Options{Root: t.TempDir(), Config: cfg, Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Failures) != 1 || !strings.Contains(f.Failures[0].Message, "oops") {
		t.Errorf("failures: %+v", f.Failures)
	}
}

func TestCountMustBePositive(t *testing.T) {
	if _, err := Run(Options{Root: t.TempDir(), Count: 0}); err == nil {
		t.Error("a count of zero should be refused")
	}
}

func TestConfigRejectsUnknownFields(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(p, []byte(`{"commandz": []}`), 0o644)
	if _, err := LoadConfig(p); err == nil {
		t.Error("a misspelled key should be an error, not silently ignored")
	}
	os.WriteFile(p, []byte(`{"commands": [{"name": "x", "dir": "d", "argv": ["a"]}], "exclude_package_suffixes": ["/y"]}`), 0o644)
	c, err := LoadConfig(p)
	if err != nil || len(c.Commands) != 1 || c.ExcludePackageSuffixes[0] != "/y" {
		t.Errorf("%+v %v", c, err)
	}
}
