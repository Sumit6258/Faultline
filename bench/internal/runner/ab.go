package runner

import (
	"bytes"
	"fmt"
	"io"
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

// ABOptions describes an interleaved comparison of two checkouts of the same repository.
type ABOptions struct {
	OldRoot, NewRoot string
	Config           Config
	Count            int
	BenchTime        string
	Pattern          string
	Only             []string
	Log              io.Writer
	Now              func() time.Time
	// Trace, if set, is called before each execution with the side ("old" or "new") and the round.
	Trace func(side string, round int)
}

type ABResult struct {
	Old, New model.File
}

type pair struct {
	module, pkg    string
	oldBin, newBin string
	oldDir, newDir string
}

type listedPkg struct{ importPath, dir string }

func listPackages(moduleDir string) ([]listedPkg, error) {
	cmd := exec.Command("go", "list", "-f", "{{.ImportPath}}|{{.Dir}}", "./...")
	cmd.Dir = moduleDir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var pkgs []listedPkg
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if i := strings.Index(line, "|"); i > 0 {
			pkgs = append(pkgs, listedPkg{line[:i], line[i+1:]})
		}
	}
	return pkgs, nil
}

func compile(pkgDir, out string) error {
	cmd := exec.Command("go", "test", "-c", "-o", out, ".")
	cmd.Dir = pkgDir
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%v: %s", err, lastLines(buf.String(), 3))
	}
	return nil
}

// RunAB builds the benchmarks of both trees once, then runs them alternately,
// old then new, new then old, round after round. Anything that changes the
// machine's speed over the course of the run, a neighbour waking up, the CPU
// changing frequency, then lands on both sides alike instead of on whichever
// side happened to run second. That is what makes the comparison trustworthy on
// a machine that is not quiet. Measured here, comparing identical code in two
// separately timed batches flagged 17 of 56 benchmarks as changed, and doing it
// interleaved flagged 2 of 56. Removing the average drift afterwards left most
// of the false changes in place, because the noise is per benchmark.
func RunAB(opts ABOptions) (ABResult, error) {
	if opts.Count < 1 {
		return ABResult{}, fmt.Errorf("count must be at least 1")
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
	// Package directories come back from go list as absolute paths, so the roots must be absolute too,
	// or relating one to the other fails, and every package looks like it lives at the module root.
	var err error
	if opts.OldRoot, err = filepath.Abs(opts.OldRoot); err != nil {
		return ABResult{}, err
	}
	if opts.NewRoot, err = filepath.Abs(opts.NewRoot); err != nil {
		return ABResult{}, err
	}
	tmp, err := os.MkdirTemp("", "faultbench-ab-")
	if err != nil {
		return ABResult{}, err
	}
	defer os.RemoveAll(tmp)

	var res ABResult
	base := model.Environment{NumCPU: runtime.NumCPU(), Date: opts.Now().UTC().Format(time.RFC3339), Count: opts.Count, BenchTime: opts.BenchTime}
	if v := strings.Fields(capture(opts.NewRoot, "go", "version")); len(v) >= 3 {
		base.GoVersion = v[2]
	}
	res.Old.Env, res.New.Env = base, base
	// The two sides can be the same directory, which is how to test the tool on identical code.
	for _, s := range []struct {
		root string
		file *model.File
	}{{opts.OldRoot, &res.Old}, {opts.NewRoot, &res.New}} {
		s.file.Env.Commit = capture(s.root, "git", "rev-parse", "--short", "HEAD")
		s.file.Env.Dirty = capture(s.root, "git", "status", "--porcelain") != ""
	}

	mods, err := FindModules(opts.NewRoot)
	if err != nil {
		return res, err
	}
	var pairs []pair
	for _, mod := range mods {
		if !wanted(mod, opts.Only) {
			continue
		}
		if _, err := os.Stat(filepath.Join(opts.OldRoot, mod, "go.mod")); err != nil {
			logf("skipping %s: it does not exist in the old tree", mod)
			continue
		}
		logf("building %s in both trees", mod)
		newPkgs, err := listPackages(filepath.Join(opts.NewRoot, mod))
		if err != nil {
			res.New.Failures = append(res.New.Failures, model.Failure{Module: mod, Message: "go list: " + err.Error()})
			continue
		}
		oldPkgs, err := listPackages(filepath.Join(opts.OldRoot, mod))
		if err != nil {
			res.Old.Failures = append(res.Old.Failures, model.Failure{Module: mod, Message: "go list: " + err.Error()})
			continue
		}
		oldByRel := map[string]listedPkg{}
		for _, p := range oldPkgs {
			rel, err := filepath.Rel(filepath.Join(opts.OldRoot, mod), p.dir)
			if err != nil {
				return res, fmt.Errorf("relating %s to %s: %w", p.dir, mod, err)
			}
			oldByRel[rel] = p
		}
		for i, np := range newPkgs {
			rel, err := filepath.Rel(filepath.Join(opts.NewRoot, mod), np.dir)
			if err != nil {
				return res, fmt.Errorf("relating %s to %s: %w", np.dir, mod, err)
			}
			op, ok := oldByRel[rel]
			if !ok {
				continue
			}
			pr := pair{module: mod, pkg: np.importPath, oldDir: op.dir, newDir: np.dir,
				oldBin: filepath.Join(tmp, fmt.Sprintf("%s-%d-old.test", strings.ReplaceAll(mod, "/", "_"), i)),
				newBin: filepath.Join(tmp, fmt.Sprintf("%s-%d-new.test", strings.ReplaceAll(mod, "/", "_"), i))}
			errOld := compile(op.dir, pr.oldBin)
			errNew := compile(np.dir, pr.newBin)
			if errNew != nil {
				res.New.Failures = append(res.New.Failures, model.Failure{Module: mod, Message: errNew.Error()})
			}
			if errOld != nil {
				res.Old.Failures = append(res.Old.Failures, model.Failure{Module: mod, Message: errOld.Error()})
			}
			_, oldStat := os.Stat(pr.oldBin)
			_, newStat := os.Stat(pr.newBin)
			if errOld == nil && errNew == nil && oldStat == nil && newStat == nil { // no test files means no binary, which is fine
				pairs = append(pairs, pr)
			}
		}
	}

	logf("measuring %d packages with benchmarks, %d rounds each", len(pairs), opts.Count)
	var colOld, colNew collector
	run := func(side string, round int, p pair) {
		bin, dir, col, file := p.oldBin, p.oldDir, &colOld, &res.Old
		if side == "new" {
			bin, dir, col, file = p.newBin, p.newDir, &colNew, &res.New
		}
		if opts.Trace != nil {
			opts.Trace(side, round)
		}
		args := []string{"-test.run=^$", "-test.bench=" + opts.Pattern, "-test.benchmem", "-test.count=1", "-test.timeout=60m"}
		if opts.BenchTime != "" {
			args = append(args, "-test.benchtime="+opts.BenchTime)
		}
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		var buf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &buf, &buf
		runErr := cmd.Run()
		text := buf.String()
		out, _ := benchfmt.Parse(strings.NewReader(text), p.pkg)
		if runErr != nil || out.Failed {
			msg := out.FailReason
			if msg == "" && runErr != nil {
				msg = runErr.Error() + ": " + lastLines(text, 3)
			}
			file.Failures = append(file.Failures, model.Failure{Module: p.module, Message: msg})
		}
		if file.Env.GOOS == "" {
			file.Env.GOOS, file.Env.GOARCH = out.GOOS, out.GOARCH
		}
		if file.Env.CPU == "" {
			file.Env.CPU = out.CPU
		}
		col.add(p.module, out, opts.Config.ExcludePackageSuffixes)
	}

	for round := 0; round < opts.Count; round++ {
		logf("round %d of %d", round+1, opts.Count)
		order := [2]string{"old", "new"}
		if round%2 == 1 {
			order = [2]string{"new", "old"} // alternate who goes first, so neither side always gets the warmer or the cooler slot
		}
		for _, p := range pairs {
			run(order[0], round, p)
			run(order[1], round, p)
		}
		for _, c := range opts.Config.Commands {
			if !wanted(c.Dir, opts.Only) {
				continue
			}
			argv := expand(c.Argv)
			for _, side := range order {
				root, col, file := opts.OldRoot, &colOld, &res.Old
				if side == "new" {
					root, col, file = opts.NewRoot, &colNew, &res.New
				}
				if opts.Trace != nil {
					opts.Trace(side, round)
				}
				cmd := exec.Command(argv[0], argv[1:]...)
				cmd.Dir = filepath.Join(root, c.Dir)
				var buf bytes.Buffer
				cmd.Stdout, cmd.Stderr = &buf, &buf
				runErr := cmd.Run()
				text := buf.String()
				if runErr != nil {
					file.Failures = append(file.Failures, model.Failure{Module: c.Dir, Message: fmt.Sprintf("%s: %v: %s", c.Name, runErr, lastLines(text, 3))})
					continue
				}
				out, _ := benchfmt.Parse(strings.NewReader(text), c.Name)
				col.add(c.Dir, out, nil)
			}
		}
	}

	// Both sides ran on this machine, so they share its description.
	if res.Old.Env.GOOS == "" {
		res.Old.Env.GOOS, res.Old.Env.GOARCH, res.Old.Env.CPU = res.New.Env.GOOS, res.New.Env.GOARCH, res.New.Env.CPU
	}
	if res.Old.Env.GOOS == "" {
		res.Old.Env.GOOS, res.Old.Env.GOARCH = runtime.GOOS, runtime.GOARCH
	}
	if res.Old.Env.CPU == "" {
		res.Old.Env.CPU = "unknown"
	}
	res.New.Env.GOOS, res.New.Env.GOARCH, res.New.Env.CPU = res.Old.Env.GOOS, res.Old.Env.GOARCH, res.Old.Env.CPU
	res.Old.Series, res.New.Series = colOld.series(), colNew.series()
	sort.SliceStable(res.Old.Failures, func(i, j int) bool { return res.Old.Failures[i].Module < res.Old.Failures[j].Module })
	return res, nil
}
