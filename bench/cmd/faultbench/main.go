// Command faultbench runs every benchmark in the repository, reports them
// together, compares two runs without being fooled by noise, and guards the
// allocation counts that noise cannot touch.
//
//	faultbench run      run everything and save the results
//	faultbench report   turn a results file into a markdown report
//	faultbench compare  say what changed between two results files, and whether it is real
//	faultbench ab       compare two checkouts by running them alternately, which survives a noisy machine
//	faultbench check    fail if any benchmark allocates more than its recorded baseline
//
// Run it from the repository root. See bench/README.md.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Sumit6258/Faultline/bench/internal/baseline"
	"github.com/Sumit6258/Faultline/bench/internal/model"
	"github.com/Sumit6258/Faultline/bench/internal/report"
	"github.com/Sumit6258/Faultline/bench/internal/runner"
	"github.com/Sumit6258/Faultline/bench/internal/stats"
)

const usage = `usage: faultbench <command> [flags]

  run      run every benchmark and save the results     faultbench run -count 5 -o results.json
  report   write a markdown report from results         faultbench report -in results.json -o docs/BENCHMARKS.md
  compare  compare two results files                    faultbench compare old.json new.json
  ab       compare two checkouts, interleaved           faultbench ab -old ../base -new .
  check    check allocation counts against a baseline   faultbench check            (or -update to record one)

Run it from the repository root. Each command takes -h for its flags.
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "run":
		return cmdRun(args[1:], stdout, stderr)
	case "report":
		return cmdReport(args[1:], stdout, stderr)
	case "compare":
		return cmdCompare(args[1:], stdout, stderr)
	case "ab":
		return cmdAB(args[1:], stdout, stderr)
	case "check":
		return cmdCheck(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
	return 2
}

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

type common struct {
	root, config, only, pattern, benchtime string
}

func (c *common) flags(fs *flag.FlagSet, defaultBenchtime string) {
	fs.StringVar(&c.root, "root", ".", "the repository root")
	fs.StringVar(&c.config, "config", "bench/benchmarks.json", "which scripts to run, and which packages to skip. Optional")
	fs.StringVar(&c.only, "only", "", "comma separated directory prefixes to limit the run to, such as labs/,systems/url-shortener")
	fs.StringVar(&c.pattern, "bench", ".", "which benchmarks to run, as go test -bench takes it")
	fs.StringVar(&c.benchtime, "benchtime", defaultBenchtime, "how long each run of a benchmark lasts, such as 200ms, or a count such as 1000x")
}

func (c *common) options(count int, stderr io.Writer) (runner.Options, error) {
	o := runner.Options{Root: c.root, Count: count, BenchTime: c.benchtime, Pattern: c.pattern, Only: split(c.only), Log: stderr}
	cfgPath := c.config
	if !strings.HasPrefix(cfgPath, "/") && c.root != "." {
		// a relative config path is relative to the repository root, not to wherever the tool was started
		if _, err := os.Stat(cfgPath); err != nil {
			cfgPath = c.root + "/" + cfgPath
		}
	}
	if _, err := os.Stat(cfgPath); err == nil {
		cfg, err := runner.LoadConfig(cfgPath)
		if err != nil {
			return o, err
		}
		o.Config = cfg
	}
	return o, nil
}

func cmdRun(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var c common
	c.flags(fs, "200ms")
	count := fs.Int("count", 5, "how many times to run each benchmark. More makes the statistics sharper")
	out := fs.String("o", "bench-results.json", "where to write the results")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opts, err := c.options(*count, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	f, err := runner.Run(opts)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := f.Save(*out); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	noisy := 0
	for _, s := range f.Series {
		if xs := s.Samples["ns/op"]; len(xs) > 0 && stats.Summarize(xs).Spread > report.NoisyThreshold {
			noisy++
		}
	}
	fmt.Fprintf(stdout, "%d benchmarks measured %d times each on %s, %d of them noisy. Saved to %s.\n", len(f.Series), *count, f.Env.CPU, noisy, *out)
	for _, fl := range f.Failures {
		fmt.Fprintf(stdout, "FAILED %s: %s\n", fl.Module, fl.Message)
	}
	if len(f.Failures) > 0 {
		return 1
	}
	return 0
}

func cmdReport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	in := fs.String("in", "bench-results.json", "the results to report on")
	out := fs.String("o", "", "write the report here. Default is standard output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	f, err := model.Load(*in)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	md := report.Markdown(f)
	if *out == "" {
		fmt.Fprint(stdout, md)
		return 0
	}
	if err := os.WriteFile(*out, []byte(md), 0o644); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	fmt.Fprintf(stdout, "wrote %s\n", *out)
	return 0
}

func cmdCompare(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	fs.SetOutput(stderr)
	threshold := fs.Float64("threshold", 5, "the smallest change worth reporting, in percent")
	alpha := fs.Float64("alpha", 0.05, "the p-value below which a difference is called real")
	force := fs.Bool("force", false, "compare timings even if the results came from different machines. They will not mean much")
	fail := fs.Bool("fail-on-regression", false, "exit 1 if anything got significantly slower or allocates more")
	out := fs.String("o", "", "also write the comparison here as markdown")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(stderr, "usage: faultbench compare [flags] old.json new.json")
		return 2
	}
	oldF, err := model.Load(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	newF, err := model.Load(fs.Arg(1))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	cmp := report.Compare(oldF, newF, report.CompareOptions{Threshold: *threshold / 100, Alpha: *alpha, Force: *force})
	md := cmp.Markdown()
	fmt.Fprint(stdout, md)
	if *out != "" {
		if err := os.WriteFile(*out, []byte(md), 0o644); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	}
	if *fail && cmp.HasRegression() {
		return 1
	}
	return 0
}

func cmdAB(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ab", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var c common
	c.flags(fs, "200ms")
	oldRoot := fs.String("old", "", "the checkout to compare against, for example one made with: git worktree add ../base main")
	newRoot := fs.String("new", ".", "the checkout with your change")
	count := fs.Int("count", 7, "how many alternating rounds. More makes the statistics sharper")
	threshold := fs.Float64("threshold", 5, "the smallest change worth reporting, in percent")
	alpha := fs.Float64("alpha", 0.05, "the p-value below which a difference is called real")
	fail := fs.Bool("fail-on-regression", false, "exit 1 if anything got significantly slower or allocates more")
	save := fs.String("o", "", "also save the raw results as PREFIX-old.json and PREFIX-new.json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *oldRoot == "" {
		fmt.Fprintln(stderr, "usage: faultbench ab -old DIR [-new DIR] [flags]\n\n-old is a second checkout of the repository: git worktree add ../base main")
		return 2
	}
	c.root = *newRoot
	opts, err := c.options(*count, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	res, err := runner.RunAB(runner.ABOptions{OldRoot: *oldRoot, NewRoot: *newRoot, Config: opts.Config, Count: *count,
		BenchTime: c.benchtime, Pattern: c.pattern, Only: opts.Only, Log: stderr})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if *save != "" {
		if err := res.Old.Save(*save + "-old.json"); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		if err := res.New.Save(*save + "-new.json"); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	}
	cmp := report.Compare(res.Old, res.New, report.CompareOptions{Threshold: *threshold / 100, Alpha: *alpha})
	fmt.Fprint(stdout, cmp.Markdown())
	for _, side := range []struct {
		name string
		f    model.File
	}{{"old", res.Old}, {"new", res.New}} {
		for _, fl := range side.f.Failures {
			fmt.Fprintf(stdout, "FAILED (%s tree) %s: %s\n", side.name, fl.Module, fl.Message)
		}
	}
	if len(res.Old.Failures)+len(res.New.Failures) > 0 {
		return 1
	}
	if *fail && cmp.HasRegression() {
		return 1
	}
	return 0
}

func cmdCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var c common
	// A fixed iteration count, not a duration: allocations per operation are amortized over the
	// iterations, so the baseline and the check must run the same number to agree.
	c.flags(fs, "1000x")
	path := fs.String("baseline", "bench/allocs-baseline.json", "the allocation baseline")
	update := fs.Bool("update", false, "record a new baseline from this machine instead of checking")
	require := fs.Bool("require-baseline", false, "fail, instead of passing quietly, if there is no baseline for this platform. For CI")
	allowMissing := fs.Bool("allow-missing", false, "do not fail because a baselined benchmark was not measured. For a deliberate removal")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *update {
		opts, err := c.options(3, stderr) // three runs, to see which counts hold still
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		f, err := runner.Run(opts)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		if len(f.Failures) > 0 {
			for _, fl := range f.Failures {
				fmt.Fprintf(stdout, "FAILED %s: %s\n", fl.Module, fl.Message)
			}
			fmt.Fprintln(stdout, "A baseline from a partial run would silently drop the missing benchmarks, so none was written.")
			return 1
		}
		b := baseline.Build(f)
		if err := b.Save(*path); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		fmt.Fprintf(stdout, "Recorded %d benchmarks to %s (%s/%s, %s).\n", len(b.Entries), *path, b.GOOS, b.GOARCH, b.GoMinor)
		if len(b.Unstable) > 0 {
			fmt.Fprintf(stdout, "%d did not allocate the same amount on every run, so they are not held to anything:\n", len(b.Unstable))
			for _, k := range b.Unstable {
				fmt.Fprintf(stdout, "  %s\n", k)
			}
		}
		return 0
	}

	b, err := baseline.Load(*path)
	if err != nil {
		if os.IsNotExist(unwrap(err)) {
			fmt.Fprintf(stdout, "No baseline at %s. Record one with `faultbench check -update`.\n", *path)
			if *require {
				return 3
			}
			return 0
		}
		fmt.Fprintln(stderr, err)
		return 2
	}
	opts, err := c.options(1, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	f, err := runner.Run(opts)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	for _, fl := range f.Failures {
		fmt.Fprintf(stdout, "FAILED %s: %s\n", fl.Module, fl.Message)
	}
	res := baseline.Check(b, f, baseline.DefaultOptions())
	fmt.Fprint(stdout, res.Text(b, *require))
	if res.Mismatch != "" {
		if *require {
			return 3
		}
		return 0
	}
	if res.Failed(*require, *allowMissing) || len(f.Failures) > 0 {
		fmt.Fprintln(stdout, "FAILED")
		return 1
	}
	fmt.Fprintln(stdout, "ok")
	return 0
}

func unwrap(err error) error {
	for {
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return err
		}
		err = u.Unwrap()
	}
}
