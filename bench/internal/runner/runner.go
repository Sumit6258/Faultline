// Package runner finds the benchmarks in a repository, runs them, and gathers
// what they print into a model.File.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Sumit6258/Faultline/bench/internal/benchfmt"
	"github.com/Sumit6258/Faultline/bench/internal/model"
)

// Command is a benchmark that is not a Go benchmark function: a script that
// prints lines in Go's benchmark format, run once per requested repetition.
type Command struct {
	Name string   `json:"name"`
	Dir  string   `json:"dir"`
	Argv []string `json:"argv"` // ${PYTHON} is replaced by $PYTHON, or python3
}

type Config struct {
	Commands []Command `json:"commands"`
	// A package whose import path ends in one of these is skipped. The vendored copies of the
	// metrics kit would otherwise be measured nine times over.
	ExcludePackageSuffixes []string `json:"exclude_package_suffixes"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	raw, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

type Options struct {
	Root      string
	Config    Config
	Count     int
	BenchTime string
	Pattern   string   // which benchmarks, as go test -bench takes it; default "."
	Only      []string // run only modules and commands whose directory starts with one of these
	Log       io.Writer
	Now       func() time.Time
}

// FindModules lists the directories under root that hold a go.mod, as slash separated paths relative to root.
func FindModules(root string) ([]string, error) {
	var mods []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "go.mod" {
			rel, _ := filepath.Rel(root, filepath.Dir(path))
			mods = append(mods, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(mods)
	return mods, err
}

func wanted(dir string, only []string) bool {
	if len(only) == 0 {
		return true
	}
	for _, p := range only {
		if strings.HasPrefix(dir, p) {
			return true
		}
	}
	return false
}

type group struct {
	module, pkg, name string
	procs             int
	samples           map[string][]float64
}

type collector struct {
	order  []string
	groups map[string]*group
}

func (c *collector) add(module string, out benchfmt.Output, excluded []string) {
	if c.groups == nil {
		c.groups = map[string]*group{}
	}
next:
	for _, s := range out.Samples {
		for _, suffix := range excluded {
			if strings.HasSuffix(s.Package, suffix) {
				continue next
			}
		}
		key := module + "|" + s.Package + "|" + s.Name
		g := c.groups[key]
		if g == nil {
			g = &group{module: module, pkg: s.Package, name: s.Name, procs: s.Procs, samples: map[string][]float64{}}
			c.groups[key] = g
			c.order = append(c.order, key)
		}
		for metric, v := range s.Metrics {
			g.samples[metric] = append(g.samples[metric], v)
		}
	}
}

func (c *collector) series() []model.Series {
	out := make([]model.Series, 0, len(c.order))
	for _, k := range c.order {
		g := c.groups[k]
		out = append(out, model.Series{Module: g.module, Package: g.pkg, Name: g.name, Procs: g.procs, Samples: g.samples})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}

func expand(argv []string) []string {
	py := os.Getenv("PYTHON")
	if py == "" {
		py = "python3"
	}
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = strings.ReplaceAll(a, "${PYTHON}", py)
	}
	return out
}

func capture(dir string, argv ...string) string {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Run executes every benchmark and returns the results.
func Run(opts Options) (model.File, error) {
	if opts.Count < 1 {
		return model.File{}, fmt.Errorf("count must be at least 1")
	}
	if opts.Pattern == "" {
		opts.Pattern = "."
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	logf := func(format string, a ...any) {
		if opts.Log != nil {
			fmt.Fprintf(opts.Log, format+"\n", a...)
		}
	}

	var f model.File
	f.Env = model.Environment{
		NumCPU:    runtime.NumCPU(),
		Date:      opts.Now().UTC().Format(time.RFC3339),
		Count:     opts.Count,
		BenchTime: opts.BenchTime,
	}
	if v := strings.Fields(capture(opts.Root, "go", "version")); len(v) >= 3 {
		f.Env.GoVersion = v[2]
	}
	f.Env.Commit = capture(opts.Root, "git", "rev-parse", "--short", "HEAD")
	f.Env.Dirty = capture(opts.Root, "git", "status", "--porcelain") != ""

	mods, err := FindModules(opts.Root)
	if err != nil {
		return f, err
	}
	var col collector
	for _, mod := range mods {
		if !wanted(mod, opts.Only) {
			continue
		}
		logf("benchmarking %s", mod)
		args := []string{"test", "-run", "^$", "-bench", opts.Pattern, "-benchmem", "-count", fmt.Sprint(opts.Count), "-p", "1", "-timeout", "60m"}
		if opts.BenchTime != "" {
			args = append(args, "-benchtime", opts.BenchTime)
		}
		args = append(args, "./...")
		cmd := exec.CommandContext(context.Background(), "go", args...)
		cmd.Dir = filepath.Join(opts.Root, mod)
		var buf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &buf, &buf
		runErr := cmd.Run()
		text := buf.String() // parsing reads the buffer to the end, so keep the text for any error message
		out, _ := benchfmt.Parse(strings.NewReader(text), mod)
		if runErr != nil || out.Failed {
			msg := out.FailReason
			if msg == "" && runErr != nil {
				msg = runErr.Error()
				if tail := lastLines(text, 3); tail != "" {
					msg += ": " + tail
				}
			}
			f.Failures = append(f.Failures, model.Failure{Module: mod, Message: msg})
		}
		if f.Env.GOOS == "" {
			f.Env.GOOS, f.Env.GOARCH = out.GOOS, out.GOARCH
		}
		if f.Env.CPU == "" {
			f.Env.CPU = out.CPU
		}
		col.add(mod, out, opts.Config.ExcludePackageSuffixes)
	}

	for _, c := range opts.Config.Commands {
		if !wanted(c.Dir, opts.Only) {
			continue
		}
		logf("benchmarking %s (%s)", c.Name, c.Dir)
		argv := expand(c.Argv)
		if f.Env.PythonVersion == "" && strings.Contains(strings.ToLower(filepath.Base(argv[0])), "python") {
			f.Env.PythonVersion = strings.TrimPrefix(capture(opts.Root, argv[0], "--version"), "Python ")
		}
		for i := 0; i < opts.Count; i++ {
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Dir = filepath.Join(opts.Root, c.Dir)
			var buf bytes.Buffer
			cmd.Stdout, cmd.Stderr = &buf, &buf
			runErr := cmd.Run()
			text := buf.String()
			out, _ := benchfmt.Parse(strings.NewReader(text), c.Name)
			if runErr != nil {
				f.Failures = append(f.Failures, model.Failure{Module: c.Dir, Message: fmt.Sprintf("%s: %v: %s", c.Name, runErr, lastLines(text, 3))})
				break
			}
			col.add(c.Dir, out, nil)
		}
	}

	if f.Env.GOOS == "" {
		f.Env.GOOS, f.Env.GOARCH = runtime.GOOS, runtime.GOARCH
	}
	if f.Env.CPU == "" {
		f.Env.CPU = "unknown"
	}
	f.Series = col.series()
	return f, nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}
